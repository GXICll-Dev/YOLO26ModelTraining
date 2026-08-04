[CmdletBinding()]
param(
    [string]$RuntimeRoot,
    [string]$ConfigPath,
    [switch]$Quiet,
    [switch]$SkipModelLoad,
    [switch]$RequireGPU
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Get-Sha256([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Resolve-PathWithin([string]$Path, [string]$Parent) {
    $fullPath = [IO.Path]::GetFullPath($Path)
    $fullParent = [IO.Path]::GetFullPath($Parent).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $prefix = $fullParent + [IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Runtime lock references a path outside $fullParent`: $fullPath"
    }
    return $fullPath
}

function Get-DirectoryBytes([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        return [int64]0
    }
    $measurement = Get-ChildItem -LiteralPath $Path -File -Recurse -Force | Measure-Object -Property Length -Sum
    if ($null -eq $measurement.Sum) {
        return [int64]0
    }
    return [int64]$measurement.Sum
}

function Normalize-PackageName([string]$Name) {
    return ([regex]::Replace($Name.Trim().ToLowerInvariant(), "[-_.]+", "-"))
}

function Parse-PinnedPackage([string]$Spec) {
    $parts = $Spec -split "==", 2
    if ($parts.Count -ne 2) {
        throw "Runtime package is not exactly pinned: $Spec"
    }
    return @{
        Name = Normalize-PackageName $parts[0]
        Version = $parts[1]
    }
}

function Invoke-NativeCapture([string]$Executable, [string[]]$Arguments) {
    $previousPreference = $ErrorActionPreference
    try {
        # Windows PowerShell 5.1 turns redirected native stderr into error
        # records. Capture it without aborting so callers can report the full
        # Python traceback together with the real process exit code.
        $ErrorActionPreference = "Continue"
        $output = & $Executable @Arguments 2>&1
        return [pscustomobject]@{
            ExitCode = $LASTEXITCODE
            Output = @($output)
        }
    } finally {
        $ErrorActionPreference = $previousPreference
    }
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
    $ConfigPath = Join-Path $scriptRoot "runtime-win-x64-cuda.lock.json"
}
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
if ([string]::IsNullOrWhiteSpace($RuntimeRoot)) {
    $RuntimeRoot = Join-Path $repoRoot "runtime"
}
$RuntimeRoot = [IO.Path]::GetFullPath($RuntimeRoot)
$ConfigPath = [IO.Path]::GetFullPath($ConfigPath)

if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
    throw "Runtime lock file not found: $ConfigPath"
}
$config = [IO.File]::ReadAllText($ConfigPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
$pythonRoot = Join-Path $RuntimeRoot "python"
$python = Join-Path $pythonRoot "python.exe"
$model = Join-Path (Join-Path $RuntimeRoot "models") ([string]$config.model.targetName)
$manifestPath = Join-Path $RuntimeRoot "runtime-manifest.json"

foreach ($requiredFile in @($python, $model, $manifestPath)) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "Portable runtime file is missing: $requiredFile"
    }
}

$manifest = [IO.File]::ReadAllText($manifestPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
if ([int]$manifest.schemaVersion -ne [int]$config.schemaVersion) {
    throw "Runtime manifest schema does not match the lock file."
}
if ([string]$manifest.runtimeId -ne [string]$config.runtimeId) {
    throw "Runtime manifest id does not match the lock file."
}

$configHash = Get-Sha256 $ConfigPath
if ([string]$manifest.configSha256 -ne $configHash) {
    throw "Runtime lock changed after this runtime was built. Run npm run runtime:prepare."
}
$prepareScript = Join-Path $scriptRoot "prepare-portable-runtime.ps1"
$verifyScriptHash = Get-Sha256 $MyInvocation.MyCommand.Path
if (-not (Test-Path -LiteralPath $prepareScript -PathType Leaf)) {
    throw "Runtime preparation script is missing: $prepareScript"
}
$prepareScriptHash = Get-Sha256 $prepareScript
if ([string]$manifest.scripts.prepareSha256 -ne $prepareScriptHash -or [string]$manifest.scripts.verifySha256 -ne $verifyScriptHash) {
    throw "Portable runtime scripts changed after this runtime was built. Run npm run runtime:prepare."
}
$modelHash = Get-Sha256 $model
if ($modelHash -ne ([string]$config.model.sha256).ToLowerInvariant()) {
    throw "Bundled model checksum mismatch: $model"
}
$appLocalRuntimeProperty = $config.PSObject.Properties["appLocalRuntime"]
if ($null -eq $appLocalRuntimeProperty -or @($appLocalRuntimeProperty.Value).Count -eq 0) {
    throw "Runtime lock does not define appLocalRuntime files."
}
$appLocalRuntimeSummary = @()
foreach ($entry in @($appLocalRuntimeProperty.Value)) {
    $target = Resolve-PathWithin (Join-Path $RuntimeRoot ([string]$entry.target).Replace("/", "\")) $RuntimeRoot
    if (-not (Test-Path -LiteralPath $target -PathType Leaf)) {
        throw "App-local runtime file is missing: $target"
    }
    $expectedHash = ([string]$entry.sha256).ToLowerInvariant()
    if ((Get-Sha256 $target) -ne $expectedHash) {
        throw "App-local runtime checksum mismatch: $target"
    }
    $appLocalRuntimeSummary += "$([string]$entry.name) $([string]$entry.version)"
}

$pthFile = Get-ChildItem -LiteralPath $pythonRoot -Filter "python*._pth" -File | Select-Object -First 1
if ($null -eq $pthFile) {
    throw "CPython embeddable path configuration is missing in $pythonRoot"
}
$pthText = [IO.File]::ReadAllText($pthFile.FullName, [Text.Encoding]::UTF8)
if ($pthText -notmatch "(?m)^import site\s*$" -or $pthText -notmatch "(?m)^Lib\\site-packages\s*$") {
    throw "CPython embeddable path configuration does not enable the bundled site-packages."
}
if (Test-Path -LiteralPath (Join-Path $RuntimeRoot "third_party")) {
    throw "The portable runtime must not contain the untrimmed third_party tree."
}
$installedChecks = Join-Path $pythonRoot "Lib\site-packages\ultralytics\utils\checks.py"
if (-not (Test-Path -LiteralPath $installedChecks -PathType Leaf)) {
    throw "Installed Ultralytics checks.py is missing: $installedChecks"
}
$installedChecksText = [IO.File]::ReadAllText($installedChecks, [Text.Encoding]::UTF8)
if ($installedChecksText -notmatch 'YOLO_MODEL_PATH') {
    throw "Installed Ultralytics AMP check is not bound to the bundled offline model."
}

$oldEnvironment = @{}
$verifyTemp = Join-Path ([IO.Path]::GetTempPath()) ("modeltraining-runtime-verify-" + [Guid]::NewGuid().ToString("N"))
[IO.Directory]::CreateDirectory($verifyTemp) | Out-Null
foreach ($name in @(
    "PATH", "PYTHONHOME", "PYTHONPATH", "PYTHONNOUSERSITE", "PYTHONDONTWRITEBYTECODE",
    "CONDA_PREFIX", "CONDA_DEFAULT_ENV", "VIRTUAL_ENV", "CUDA_VISIBLE_DEVICES",
    "YOLO_CONFIG_DIR", "MPLCONFIGDIR", "YOLO_OFFLINE", "ULTRALYTICS_OFFLINE"
)) {
    $oldEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

try {
    $env:PATH = @(
        $pythonRoot,
        (Join-Path $pythonRoot "Scripts"),
        (Join-Path $env:SystemRoot "System32"),
        $env:SystemRoot,
        (Join-Path $env:SystemRoot "System32\Wbem")
    ) -join [IO.Path]::PathSeparator
    foreach ($name in @("PYTHONHOME", "PYTHONPATH", "CONDA_PREFIX", "CONDA_DEFAULT_ENV", "VIRTUAL_ENV", "CUDA_VISIBLE_DEVICES", "ULTRALYTICS_OFFLINE")) {
        Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue
    }
    $env:PYTHONNOUSERSITE = "1"
    $env:PYTHONDONTWRITEBYTECODE = "1"
    $env:YOLO_CONFIG_DIR = Join-Path $verifyTemp "ultralytics"
    $env:MPLCONFIGDIR = Join-Path $verifyTemp "matplotlib"
    [IO.Directory]::CreateDirectory($env:YOLO_CONFIG_DIR) | Out-Null
    [IO.Directory]::CreateDirectory($env:MPLCONFIGDIR) | Out-Null
    $env:YOLO_OFFLINE = "true"

    $pipCheck = Invoke-NativeCapture $python @("-m", "pip", "check")
    if ($pipCheck.ExitCode -ne 0) {
        throw "pip check failed:`n$($pipCheck.Output -join [Environment]::NewLine)"
    }

    $pipList = Invoke-NativeCapture $python @("-m", "pip", "list", "--disable-pip-version-check", "--format=json")
    if ($pipList.ExitCode -ne 0) {
        throw "Could not inspect installed runtime packages:`n$($pipList.Output -join [Environment]::NewLine)"
    }
    $installed = @{}
    foreach ($item in (($pipList.Output -join "`n") | ConvertFrom-Json)) {
        $installed[(Normalize-PackageName ([string]$item.name))] = [string]$item.version
    }

    $expectedPackages = @($config.packages)
    $expectedPackages += "pip==$($config.pip.version)"
    $expectedPackages += "ultralytics==$($config.ultralytics.version)"
$runtimeWheelsProperty = $config.PSObject.Properties["runtimeWheels"]
if ($null -eq $runtimeWheelsProperty) {
    $runtimeWheelsProperty = $config.PSObject.Properties["cpuWheels"]
}
if ($null -eq $runtimeWheelsProperty) {
    throw "Runtime lock does not define runtimeWheels."
}
    foreach ($wheel in @($runtimeWheelsProperty.Value)) {
        $expectedPackages += "$($wheel.package)==$($wheel.version)"
    }
    foreach ($spec in $expectedPackages) {
        $pin = Parse-PinnedPackage ([string]$spec)
        if (-not $installed.ContainsKey($pin.Name)) {
            throw "Pinned runtime package is missing: $spec"
        }
        if ($installed[$pin.Name] -ne $pin.Version) {
            throw "Runtime package version mismatch for $($pin.Name): expected $($pin.Version), got $($installed[$pin.Name])"
        }
    }

    $probeScript = @'
import json
import platform
import sys
import ctypes
import cv2
import torch
import torchvision
import ultralytics

result = {
    'python': platform.python_version(),
    'architecture': platform.architecture()[0],
    'machine': platform.machine(),
    'executable': sys.executable,
    'torch': torch.__version__,
    'torchvision': torchvision.__version__,
    'opencv': cv2.__version__,
    'ultralytics': ultralytics.__version__,
    'cudaAvailable': bool(torch.cuda.is_available()),
    'torchCuda': torch.version.cuda,
    'cudaDeviceCount': 0,
    'cudaDeviceNames': [],
    'cudaArchitectures': [],
    'cpuFallbackVerified': False,
    'gpuSmokeVerified': False,
    'gpuError': '',
    'modelLoaded': False,
    'msvcp140Path': '',
}
if sys.platform == 'win32':
    msvcp = ctypes.WinDLL('MSVCP140.dll')
    module_path = ctypes.create_unicode_buffer(32768)
    if ctypes.windll.kernel32.GetModuleFileNameW(ctypes.c_void_p(msvcp._handle), module_path, len(module_path)):
        result['msvcp140Path'] = module_path.value
cpu_value = (torch.arange(8, dtype=torch.float32) * 2).sum().item()
result['cpuFallbackVerified'] = cpu_value == 56.0
if result['cudaAvailable']:
    try:
        result['cudaDeviceCount'] = int(torch.cuda.device_count())
        result['cudaDeviceNames'] = [torch.cuda.get_device_name(i) for i in range(result['cudaDeviceCount'])]
        result['cudaArchitectures'] = list(torch.cuda.get_arch_list())
        value = (torch.ones((32, 32), device='cuda') @ torch.ones((32, 32), device='cuda')).sum().item()
        torch.cuda.synchronize()
        result['gpuSmokeVerified'] = value == 32768.0
    except Exception as exc:
        result['gpuError'] = str(exc)
if len(sys.argv) > 1 and sys.argv[1]:
    from ultralytics import YOLO
    YOLO(sys.argv[1])
    result['modelLoaded'] = True
print('__MODELTRAINING_RUNTIME__' + json.dumps(result, ensure_ascii=True))
'@
    $probeModel = if ($SkipModelLoad) { "" } else { $model }
    $probeResult = Invoke-NativeCapture $python @("-c", $probeScript, $probeModel)
    if ($probeResult.ExitCode -ne 0) {
        throw "Portable Python import/model probe failed:`n$($probeResult.Output -join [Environment]::NewLine)"
    }
    $markerLine = @($probeResult.Output | Where-Object { "$_" -like "__MODELTRAINING_RUNTIME__*" } | Select-Object -Last 1)
    if ($markerLine.Count -ne 1) {
        throw "Portable Python probe did not return its JSON marker:`n$($probeResult.Output -join [Environment]::NewLine)"
    }
    $probe = ([string]$markerLine[0]).Substring("__MODELTRAINING_RUNTIME__".Length) | ConvertFrom-Json
    if ([string]$probe.python -ne [string]$config.python.version) {
        throw "Portable Python version mismatch: expected $($config.python.version), got $($probe.python)"
    }
    if ([string]$probe.architecture -ne "64bit") {
        throw "Portable Python is not 64-bit."
    }
    $expectedMSVCP = [IO.Path]::GetFullPath((Join-Path $pythonRoot "MSVCP140.dll"))
    if (-not [string]::Equals([IO.Path]::GetFullPath([string]$probe.msvcp140Path), $expectedMSVCP, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Python loaded MSVCP140.dll outside the portable runtime: $($probe.msvcp140Path)"
    }
$acceleratorFlavor = "cpu"
$expectedCUDA = ""
if ($null -ne $config.PSObject.Properties["accelerator"]) {
        $acceleratorFlavor = [string]$config.accelerator.flavor
        $expectedCUDA = [string]$config.accelerator.cudaVersion
    }
    if ($acceleratorFlavor -eq "cuda") {
        if ([string]$probe.torchCuda -ne $expectedCUDA) {
            throw "Bundled PyTorch CUDA version mismatch: expected $expectedCUDA, got $($probe.torchCuda)"
        }
        if (-not [bool]$probe.cpuFallbackVerified) {
            throw "The CUDA-enabled PyTorch runtime failed its CPU fallback tensor operation."
        }
        if ([bool]$probe.cudaAvailable -and -not [bool]$probe.gpuSmokeVerified) {
            throw "The CUDA device was detected but its tensor smoke test failed: $($probe.gpuError)"
        }
        if ($RequireGPU -and -not [bool]$probe.cudaAvailable) {
            throw "A working NVIDIA CUDA device is required for this verification, but torch.cuda.is_available() is false."
        }
    } elseif ([bool]$probe.cudaAvailable -or $null -ne $probe.torchCuda) {
        throw "The bundled PyTorch build is not CPU-only."
    }
    if (-not $SkipModelLoad -and -not [bool]$probe.modelLoaded) {
        throw "The bundled YOLO26 model was not loaded."
    }

    if (@($manifest.pruning.rules | Where-Object { "$_" -eq "**/__pycache__" }).Count -gt 0) {
        $unexpectedCache = Get-ChildItem -LiteralPath $pythonRoot -Directory -Filter "__pycache__" -Recurse -Force |
            Select-Object -First 1
        if ($null -ne $unexpectedCache) {
            throw "Runtime pruning promised no __pycache__ directories, but found: $($unexpectedCache.FullName)"
        }
    }

    $pythonBytes = Get-DirectoryBytes $pythonRoot
    $modelBytes = Get-DirectoryBytes (Join-Path $RuntimeRoot "models")
    $summary = [ordered]@{
        RuntimeRoot = $RuntimeRoot
        Python = [string]$probe.python
        Torch = [string]$probe.torch
        Torchvision = [string]$probe.torchvision
        OpenCV = [string]$probe.opencv
        Ultralytics = [string]$probe.ultralytics
        AppLocalRuntime = $appLocalRuntimeSummary -join "; "
        MSVCP140Path = [string]$probe.msvcp140Path
        Accelerator = $acceleratorFlavor
        CompiledCUDA = [string]$probe.torchCuda
        CUDAAvailable = [bool]$probe.cudaAvailable
        CUDADeviceCount = [int]$probe.cudaDeviceCount
        CUDADeviceNames = @($probe.cudaDeviceNames) -join "; "
        CUDAArchitectures = @($probe.cudaArchitectures) -join ", "
        CPUFallbackVerified = [bool]$probe.cpuFallbackVerified
        GPUSmokeVerified = [bool]$probe.gpuSmokeVerified
        ModelLoaded = [bool]$probe.modelLoaded
        PythonMiB = [math]::Round($pythonBytes / 1MB, 1)
        ModelsMiB = [math]::Round($modelBytes / 1MB, 1)
        TotalMiB = [math]::Round(($pythonBytes + $modelBytes) / 1MB, 1)
    }
    if (-not $Quiet) {
        Write-Host "Portable runtime verified."
        $summary | Format-List | Out-Host
    }
    $summary
} finally {
    foreach ($name in $oldEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $oldEnvironment[$name], "Process")
    }
    if (Test-Path -LiteralPath $verifyTemp) {
        Remove-Item -LiteralPath $verifyTemp -Recurse -Force
    }
}
