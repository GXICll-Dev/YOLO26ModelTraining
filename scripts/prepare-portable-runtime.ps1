[CmdletBinding()]
param(
    [string]$ConfigPath,
    [switch]$Force,
    [switch]$Offline
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Add-Type -AssemblyName System.IO.Compression.FileSystem

function Get-Sha256([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-StringSha256([string]$Value) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($Value)
        return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Assert-PathWithin([string]$Path, [string]$Parent) {
    $fullPath = [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $fullParent = [IO.Path]::GetFullPath($Parent).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $prefix = $fullParent + [IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to mutate a path outside $fullParent`: $fullPath"
    }
    return $fullPath
}

function Remove-GeneratedTree([string]$Path, [string]$Parent) {
    $safePath = Assert-PathWithin $Path $Parent
    if (Test-Path -LiteralPath $safePath) {
        Remove-Item -LiteralPath $safePath -Recurse -Force
    }
}

function Get-VerifiedDownload([string]$Url, [string]$Destination, [string]$ExpectedSha256, [string]$CacheRoot) {
    $Destination = Assert-PathWithin $Destination $CacheRoot
    $ExpectedSha256 = $ExpectedSha256.ToLowerInvariant()
    if (Test-Path -LiteralPath $Destination -PathType Leaf) {
        if ((Get-Sha256 $Destination) -eq $ExpectedSha256) {
            Write-Host "Using cached $([IO.Path]::GetFileName($Destination))"
            return $Destination
        }
        Remove-Item -LiteralPath $Destination -Force
    }
    if ($Offline) {
        throw "Offline runtime build is missing a verified cache file: $Destination"
    }
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Destination)) | Out-Null
    $partial = "$Destination.partial-$([Guid]::NewGuid().ToString('N'))"
    $partial = Assert-PathWithin $partial $CacheRoot
    try {
        Write-Host "Downloading $Url"
        $curl = Get-Command "curl.exe" -ErrorAction SilentlyContinue
        if ($null -ne $curl) {
            & $curl.Source --fail --location --silent --show-error --retry 5 --retry-delay 2 --connect-timeout 30 --output $partial $Url
            if ($LASTEXITCODE -ne 0) {
                throw "curl.exe failed to download $Url (exit code $LASTEXITCODE)"
            }
        } else {
            $downloaded = $false
            for ($attempt = 1; $attempt -le 3 -and -not $downloaded; $attempt++) {
                try {
                    Invoke-WebRequest -Uri $Url -OutFile $partial -UseBasicParsing
                    $downloaded = $true
                } catch {
                    if ($attempt -eq 3) {
                        throw
                    }
                    Start-Sleep -Seconds (2 * $attempt)
                }
            }
        }
        $actual = Get-Sha256 $partial
        if ($actual -ne $ExpectedSha256) {
            throw "Checksum mismatch for $Url`: expected $ExpectedSha256, got $actual"
        }
        # A previous timed-out shell can leave its child builder running. If
        # that builder won the race, accept only its fully verified artifact.
        if (Test-Path -LiteralPath $Destination -PathType Leaf) {
            if ((Get-Sha256 $Destination) -eq $ExpectedSha256) {
                Remove-Item -LiteralPath $partial -Force
                return $Destination
            }
            Remove-Item -LiteralPath $Destination -Force
        }
        Move-Item -LiteralPath $partial -Destination $Destination
    } finally {
        if (Test-Path -LiteralPath $partial) {
            Remove-Item -LiteralPath $partial -Force
        }
    }
    return $Destination
}

function Get-UltralyticsFingerprint([string]$PackageRoot) {
    $entries = foreach ($file in (Get-ChildItem -LiteralPath $PackageRoot -File -Recurse -Force | Sort-Object FullName)) {
        if ($file.Extension -eq ".pyc" -or $file.FullName -match "[\\/]__pycache__[\\/]") {
            continue
        }
        $relative = $file.FullName.Substring($PackageRoot.Length).TrimStart("\", "/").Replace("\", "/")
        "$relative|$($file.Length)|$(Get-Sha256 $file.FullName)"
    }
    return Get-StringSha256 ($entries -join "`n")
}

function Write-Utf8Json([object]$Value, [string]$Path) {
    $json = $Value | ConvertTo-Json -Depth 12
    [IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

function Invoke-Python([string]$Python, [string[]]$Arguments, [string]$FailureMessage) {
    & $Python @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FailureMessage (exit code $LASTEXITCODE)"
    }
}

function Remove-RuntimeBuildArtifacts([string]$PythonRoot, [string]$StagingRoot) {
    $removedBytes = [int64]0
    $removedFiles = 0
    $torchRoot = Join-Path $PythonRoot "Lib\site-packages\torch"

    # PyTorch's Windows wheel includes import/static libraries for developers
    # linking C++ extensions. Python training and inference load the adjacent
    # DLLs and do not use these .lib files at runtime.
    $torchLib = Join-Path $torchRoot "lib"
    if (Test-Path -LiteralPath $torchLib -PathType Container) {
        foreach ($file in (Get-ChildItem -LiteralPath $torchLib -File -Filter "*.lib" -Force)) {
            $safeFile = Assert-PathWithin $file.FullName $StagingRoot
            $removedBytes += $file.Length
            $removedFiles++
            Remove-Item -LiteralPath $safeFile -Force
        }
    }

    $torchInclude = Join-Path $torchRoot "include"
    if (Test-Path -LiteralPath $torchInclude -PathType Container) {
        $measurement = Get-ChildItem -LiteralPath $torchInclude -File -Recurse -Force | Measure-Object Length -Sum
        if ($null -ne $measurement.Sum) {
            $removedBytes += [int64]$measurement.Sum
        }
        $removedFiles += @(Get-ChildItem -LiteralPath $torchInclude -File -Recurse -Force).Count
        Remove-GeneratedTree $torchInclude $StagingRoot
    }

    $protoc = Join-Path $torchRoot "bin\protoc.exe"
    if (Test-Path -LiteralPath $protoc -PathType Leaf) {
        $protocFile = Get-Item -LiteralPath $protoc
        $removedBytes += $protocFile.Length
        $removedFiles++
        Remove-Item -LiteralPath (Assert-PathWithin $protoc $StagingRoot) -Force
    }

    # Bytecode caches are safe to regenerate and are especially wasteful in
    # a compressed desktop distribution. Python continues from bundled .py
    # sources when the installed resources directory is read-only.
    $cacheDirectories = @(Get-ChildItem -LiteralPath $PythonRoot -Directory -Filter "__pycache__" -Recurse -Force |
        Sort-Object { $_.FullName.Length } -Descending)
    foreach ($cacheDirectory in $cacheDirectories) {
        if (-not (Test-Path -LiteralPath $cacheDirectory.FullName -PathType Container)) {
            continue
        }
        $cacheFiles = @(Get-ChildItem -LiteralPath $cacheDirectory.FullName -File -Recurse -Force)
        $measurement = $cacheFiles | Measure-Object Length -Sum
        if ($null -ne $measurement.Sum) {
            $removedBytes += [int64]$measurement.Sum
        }
        $removedFiles += $cacheFiles.Count
        Remove-GeneratedTree $cacheDirectory.FullName $StagingRoot
    }

    return [pscustomobject]@{
        RemovedBytes = $removedBytes
        RemovedFiles = $removedFiles
        Rules = @("torch/lib/*.lib", "torch/include", "torch/bin/protoc.exe", "**/__pycache__")
    }
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
    $ConfigPath = Join-Path $scriptRoot "runtime-win-x64-cuda.lock.json"
}
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
$ConfigPath = [IO.Path]::GetFullPath($ConfigPath)
$runtimeRoot = Join-Path $repoRoot "runtime"
$cacheRoot = Join-Path $repoRoot ".cache\portable-runtime"
$verifyScript = Join-Path $scriptRoot "verify-portable-runtime.ps1"

if ($env:OS -ne "Windows_NT" -or -not [Environment]::Is64BitOperatingSystem) {
    throw "This lock builds the Windows x64 portable runtime and must run on 64-bit Windows."
}
if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
    throw "Runtime lock file not found: $ConfigPath"
}
$config = [IO.File]::ReadAllText($ConfigPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
if ([int]$config.schemaVersion -ne 1) {
    throw "Unsupported runtime lock schema: $($config.schemaVersion)"
}

$ultralyticsSource = Join-Path $repoRoot ([string]$config.ultralytics.source).Replace("/", "\")
$ultralyticsPackage = Join-Path $ultralyticsSource "ultralytics"
$modelSource = Join-Path $repoRoot ([string]$config.model.source).Replace("/", "\")
foreach ($required in @($ultralyticsPackage, (Join-Path $ultralyticsSource "pyproject.toml"), $modelSource)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "Required portable-runtime input is missing: $required"
    }
}
if ((Get-Sha256 $modelSource) -ne ([string]$config.model.sha256).ToLowerInvariant()) {
    throw "Source model checksum does not match the runtime lock: $modelSource"
}
$appLocalRuntimeProperty = $config.PSObject.Properties["appLocalRuntime"]
if ($null -eq $appLocalRuntimeProperty -or @($appLocalRuntimeProperty.Value).Count -eq 0) {
    throw "Runtime lock does not define appLocalRuntime files."
}
$appLocalRuntimeFiles = foreach ($entry in @($appLocalRuntimeProperty.Value)) {
    $source = ""
    $sourceProperty = $entry.PSObject.Properties["source"]
    $urlProperty = $entry.PSObject.Properties["url"]
    $fileNameProperty = $entry.PSObject.Properties["fileName"]
    $sourceValue = if ($null -ne $sourceProperty) { [string]$sourceProperty.Value } else { "" }
    $urlValue = if ($null -ne $urlProperty) { [string]$urlProperty.Value } else { "" }
    $fileNameValue = if ($null -ne $fileNameProperty) { [string]$fileNameProperty.Value } else { "" }
    if (-not [string]::IsNullOrWhiteSpace($sourceValue)) {
        $source = [IO.Path]::GetFullPath((Join-Path $repoRoot $sourceValue.Replace("/", "\")))
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "App-local runtime source is missing: $source"
        }
    } elseif (-not [string]::IsNullOrWhiteSpace($urlValue) -and -not [string]::IsNullOrWhiteSpace($fileNameValue)) {
        $source = Get-VerifiedDownload $urlValue (Join-Path $cacheRoot ("downloads\" + $fileNameValue)) ([string]$entry.sha256) $cacheRoot
    } else {
        throw "App-local runtime entry must define source or url/fileName: $([string]$entry.name)"
    }
    $expectedHash = ([string]$entry.sha256).ToLowerInvariant()
    if ((Get-Sha256 $source) -ne $expectedHash) {
        throw "App-local runtime checksum mismatch: $source"
    }
    [pscustomobject]@{
        Name = [string]$entry.name
        Source = $source
        Target = ([string]$entry.target).Replace("/", "\")
        Version = [string]$entry.version
        Sha256 = $expectedHash
    }
}

$configHash = Get-Sha256 $ConfigPath
$sourceFingerprint = Get-UltralyticsFingerprint $ultralyticsPackage
$prepareScriptHash = Get-Sha256 $MyInvocation.MyCommand.Path
$verifyScriptHash = Get-Sha256 $verifyScript
$fingerprint = Get-StringSha256 "$configHash|$sourceFingerprint|$prepareScriptHash|$verifyScriptHash"
$manifestPath = Join-Path $runtimeRoot "runtime-manifest.json"

if (-not $Force -and (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    try {
        $existingManifest = [IO.File]::ReadAllText($manifestPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
        if ([string]$existingManifest.fingerprint -eq $fingerprint) {
            & $verifyScript -RuntimeRoot $runtimeRoot -ConfigPath $ConfigPath -Quiet | Out-Null
            Write-Host "Portable runtime is current; skipping rebuild. Use -Force to rebuild it."
            exit 0
        }
    } catch {
        Write-Warning "Existing runtime failed validation and will be rebuilt: $($_.Exception.Message)"
    }
}

[IO.Directory]::CreateDirectory($runtimeRoot) | Out-Null
[IO.Directory]::CreateDirectory($cacheRoot) | Out-Null
$mutexName = "Global\ModelTrainingPortableRuntime-" + (Get-StringSha256 $repoRoot).Substring(0, 24)
$buildMutex = [Threading.Mutex]::new($false, $mutexName)
if (-not $buildMutex.WaitOne(0)) {
    $buildMutex.Dispose()
    throw "Another portable-runtime build is already running for this workspace."
}
foreach ($staleStaging in (Get-ChildItem -LiteralPath $runtimeRoot -Directory -Filter ".staging-*" -ErrorAction SilentlyContinue)) {
    Remove-GeneratedTree $staleStaging.FullName $runtimeRoot
}
$downloads = Join-Path $cacheRoot "downloads"
$wheelhouse = Join-Path $cacheRoot ("wheels\" + $configHash)
[IO.Directory]::CreateDirectory($downloads) | Out-Null
[IO.Directory]::CreateDirectory($wheelhouse) | Out-Null
foreach ($partialWheel in (Get-ChildItem -LiteralPath $wheelhouse -File -Filter "*.partial-*" -ErrorAction SilentlyContinue)) {
    Remove-Item -LiteralPath (Assert-PathWithin $partialWheel.FullName $cacheRoot) -Force
}

# Runtime-lock metadata such as app-local DLLs can change without changing any
# Python wheel. Seed the new content-addressed wheelhouse from the last verified
# runtime so a metadata-only update does not download multi-gigabyte CUDA wheels
# again. Exact pinned specs and locked torch/torchvision hashes are still checked
# below before any wheel is installed.
if (Test-Path -LiteralPath $manifestPath -PathType Leaf) {
    try {
        $previousManifest = [IO.File]::ReadAllText($manifestPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
        $previousWheelhouse = Join-Path $cacheRoot ("wheels\" + [string]$previousManifest.configSha256)
        if (-not [string]::Equals([IO.Path]::GetFullPath($previousWheelhouse), [IO.Path]::GetFullPath($wheelhouse), [StringComparison]::OrdinalIgnoreCase) -and
            (Test-Path -LiteralPath $previousWheelhouse -PathType Container)) {
            foreach ($cachedWheel in (Get-ChildItem -LiteralPath $previousWheelhouse -File -Filter "*.whl")) {
                $seededWheel = Join-Path $wheelhouse $cachedWheel.Name
                if (Test-Path -LiteralPath $seededWheel -PathType Leaf) {
                    continue
                }
                try {
                    New-Item -ItemType HardLink -Path $seededWheel -Target $cachedWheel.FullName -ErrorAction Stop | Out-Null
                } catch {
                    Copy-Item -LiteralPath $cachedWheel.FullName -Destination $seededWheel
                }
            }
        }
    } catch {
        Write-Warning "Could not seed the wheel cache from the previous runtime: $($_.Exception.Message)"
    }
}

$pythonArchive = Get-VerifiedDownload ([string]$config.python.url) (Join-Path $downloads ([string]$config.python.archiveName)) ([string]$config.python.sha256) $cacheRoot
$pipWheel = Get-VerifiedDownload ([string]$config.pip.url) (Join-Path $downloads ([string]$config.pip.wheelName)) ([string]$config.pip.sha256) $cacheRoot
$runtimeWheelPaths = @()
foreach ($wheel in @($config.runtimeWheels)) {
    $runtimeWheelPaths += Get-VerifiedDownload ([string]$wheel.url) (Join-Path $wheelhouse ([string]$wheel.fileName)) ([string]$wheel.sha256) $cacheRoot
}

$stagingRoot = Join-Path $runtimeRoot (".staging-" + [Guid]::NewGuid().ToString("N"))
$backupRoot = Join-Path $runtimeRoot (".backup-" + [Guid]::NewGuid().ToString("N"))
$stagingRoot = Assert-PathWithin $stagingRoot $runtimeRoot
$backupRoot = Assert-PathWithin $backupRoot $runtimeRoot
$swapped = $false

try {
    $stagingPython = Join-Path $stagingRoot "python"
    $sitePackages = Join-Path $stagingPython "Lib\site-packages"
    $stagingModels = Join-Path $stagingRoot "models"
    [IO.Directory]::CreateDirectory($sitePackages) | Out-Null
    [IO.Directory]::CreateDirectory($stagingModels) | Out-Null

    Write-Host "Extracting CPython $($config.python.version) embeddable runtime"
    [IO.Compression.ZipFile]::ExtractToDirectory($pythonArchive, $stagingPython)
    Write-Host "Installing pinned app-local Microsoft C++ runtime"
    foreach ($entry in $appLocalRuntimeFiles) {
        $target = Assert-PathWithin (Join-Path $stagingRoot $entry.Target) $stagingRoot
        [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
        Copy-Item -LiteralPath $entry.Source -Destination $target -Force
    }
    $pthFile = Get-ChildItem -LiteralPath $stagingPython -Filter "python*._pth" -File | Select-Object -First 1
    if ($null -eq $pthFile) {
        throw "CPython embeddable archive did not contain python*._pth"
    }
    $pthLines = @([IO.File]::ReadAllLines($pthFile.FullName, [Text.Encoding]::UTF8) | Where-Object {
        $_ -notmatch "^#?import site\s*$" -and $_ -notmatch "^Lib[\\/]site-packages\s*$"
    })
    $pthLines += "Lib\site-packages"
    $pthLines += "import site"
    [IO.File]::WriteAllLines($pthFile.FullName, $pthLines, [Text.UTF8Encoding]::new($false))

    Write-Host "Bootstrapping pinned pip $($config.pip.version)"
    [IO.Compression.ZipFile]::ExtractToDirectory($pipWheel, $sitePackages)
    $stagingPythonExe = Join-Path $stagingPython "python.exe"

    if (-not $Offline) {
        $downloadArgs = @(
            "-m", "pip", "download",
            "--disable-pip-version-check",
            "--no-input",
            "--no-cache-dir",
            "--only-binary=:all:",
            "--no-deps",
            "--index-url", [string]$config.packageIndex,
            "--dest", $wheelhouse
        ) + @($config.packages | ForEach-Object { [string]$_ })
        Invoke-Python $stagingPythonExe $downloadArgs "Downloading pinned runtime wheels failed"
    }

    $installArgs = @(
        "-m", "pip", "install",
        "--disable-pip-version-check",
        "--no-input",
        "--no-warn-script-location",
        "--no-index",
        "--find-links", $wheelhouse,
        "--no-deps"
    ) + @($config.packages | ForEach-Object { [string]$_ }) + $runtimeWheelPaths
    Invoke-Python $stagingPythonExe $installArgs "Installing the pinned runtime wheels failed"

    $ultralyticsBuild = Join-Path $stagingRoot "_ultralytics-source"
    [IO.Directory]::CreateDirectory($ultralyticsBuild) | Out-Null
    Copy-Item -LiteralPath $ultralyticsPackage -Destination (Join-Path $ultralyticsBuild "ultralytics") -Recurse
    foreach ($name in @("pyproject.toml", "README.md", "LICENSE")) {
        $sourceFile = Join-Path $ultralyticsSource $name
        if (Test-Path -LiteralPath $sourceFile -PathType Leaf) {
            Copy-Item -LiteralPath $sourceFile -Destination $ultralyticsBuild
        }
    }
    # The portable runtime does not need OpenCV's Qt/GUI DLLs. Keep the local
    # package metadata truthful so `pip check` accepts the headless provider.
    $opencvPin = @($config.packages | Where-Object { "$_" -like "opencv-python-headless==*" } | Select-Object -First 1)
    if ($opencvPin.Count -ne 1) {
        throw "The runtime lock must contain one pinned opencv-python-headless package."
    }
    $stagedPyproject = Join-Path $ultralyticsBuild "pyproject.toml"
    $pyprojectText = [IO.File]::ReadAllText($stagedPyproject, [Text.Encoding]::UTF8)
    $patchedPyproject = $pyprojectText.Replace('"opencv-python>=4.6.0"', ('"' + [string]$opencvPin[0] + '"'))
    if ($patchedPyproject -eq $pyprojectText) {
        throw "Could not replace the Ultralytics OpenCV dependency in the staged pyproject.toml."
    }
    [IO.File]::WriteAllText($stagedPyproject, $patchedPyproject, [Text.UTF8Encoding]::new($false))

    # GitHub release assets are not reliably reachable on deployment networks.
    # Avoid an online Arial download during dataset checks and select a local
    # CJK font when Windows provides one, otherwise Matplotlib's DejaVu Sans.
    $stagedChecks = Join-Path $ultralyticsBuild "ultralytics\utils\checks.py"
    $checksText = [IO.File]::ReadAllText($stagedChecks, [Text.Encoding]::UTF8)
    $fontAnchor = "    # Download to USER_CONFIG_DIR if missing"
    $fontFallback = @'
    # Portable/offline fallback: never require a font download to start training.
    for family in ("Microsoft YaHei", "SimHei", "SimSun", "Noto Sans CJK SC", "Arial Unicode MS", "DejaVu Sans"):
        try:
            candidate = Path(font_manager.findfont(family, fallback_to_default=False))
            if candidate.exists():
                return candidate
        except Exception:
            pass

'@
    $patchedChecks = $checksText.Replace($fontAnchor, $fontFallback + $fontAnchor)
    if ($patchedChecks -eq $checksText) {
        throw "Could not add the offline font fallback to the staged Ultralytics source."
    }
    $ampModelAnchor = 'assert amp_allclose(YOLO("yolo26n.pt"), im)'
    $ampModelReplacement = 'assert amp_allclose(YOLO(os.getenv("YOLO_MODEL_PATH", "yolo26n.pt")), im)'
    $offlineChecks = $patchedChecks.Replace($ampModelAnchor, $ampModelReplacement)
    if ($offlineChecks -eq $patchedChecks) {
        throw "Could not bind the Ultralytics AMP check to the bundled YOLO model."
    }
    [IO.File]::WriteAllText($stagedChecks, $offlineChecks, [Text.UTF8Encoding]::new($false))
    $installUltralyticsArgs = @(
        "-m", "pip", "install",
        "--disable-pip-version-check",
        "--no-input",
        "--no-cache-dir",
        "--no-warn-script-location",
        "--no-index",
        "--find-links", $wheelhouse,
        "--no-deps",
        "--no-build-isolation",
        $ultralyticsBuild
    )
    Invoke-Python $stagingPythonExe $installUltralyticsArgs "Installing the local Ultralytics source failed"
    Remove-GeneratedTree $ultralyticsBuild $stagingRoot

    Copy-Item -LiteralPath $modelSource -Destination (Join-Path $stagingModels ([string]$config.model.targetName))

    $pipListOutput = & $stagingPythonExe -m pip list --disable-pip-version-check --format=json 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Could not create the runtime package manifest: $($pipListOutput -join [Environment]::NewLine)"
    }
    $packages = ($pipListOutput -join "`n") | ConvertFrom-Json

    # This must be the final non-isolated Python invocation. The verifier runs
    # with PYTHONDONTWRITEBYTECODE=1 and asserts that pruning stays effective.
    Write-Host "Removing deterministic build-only files from the portable runtime"
    $pruning = Remove-RuntimeBuildArtifacts $stagingPython $stagingRoot

    $wheelFiles = foreach ($file in (Get-ChildItem -LiteralPath $wheelhouse -Filter "*.whl" -File | Sort-Object Name)) {
        [ordered]@{
            file = $file.Name
            sha256 = Get-Sha256 $file.FullName
            bytes = $file.Length
        }
    }
    $manifest = [ordered]@{
        schemaVersion = [int]$config.schemaVersion
        runtimeId = [string]$config.runtimeId
        createdAtUtc = [DateTime]::UtcNow.ToString("o")
        fingerprint = $fingerprint
        configSha256 = $configHash
        ultralyticsSourceSha256 = $sourceFingerprint
        scripts = [ordered]@{
            prepareSha256 = $prepareScriptHash
            verifySha256 = $verifyScriptHash
        }
        accelerator = $config.accelerator
        appLocalRuntime = @($appLocalRuntimeFiles | ForEach-Object {
            [ordered]@{
                name = $_.Name
                target = $_.Target.Replace("\", "/")
                version = $_.Version
                sha256 = $_.Sha256
            }
        })
        pruning = [ordered]@{
            removedBytes = [int64]$pruning.RemovedBytes
            removedFiles = [int]$pruning.RemovedFiles
            rules = @($pruning.Rules)
        }
        python = [ordered]@{
            version = [string]$config.python.version
            archiveSha256 = [string]$config.python.sha256
        }
        model = [ordered]@{
            file = "models/$($config.model.targetName)"
            sha256 = [string]$config.model.sha256
        }
        packages = @($packages | Sort-Object name)
        wheels = @($wheelFiles)
    }
    Write-Utf8Json $manifest (Join-Path $stagingRoot "runtime-manifest.json")

    & $verifyScript -RuntimeRoot $stagingRoot -ConfigPath $ConfigPath -Quiet | Out-Null

    [IO.Directory]::CreateDirectory($backupRoot) | Out-Null
    foreach ($name in @("python", "models", "runtime-manifest.json")) {
        $target = Join-Path $runtimeRoot $name
        if (Test-Path -LiteralPath $target) {
            Move-Item -LiteralPath (Assert-PathWithin $target $runtimeRoot) -Destination (Join-Path $backupRoot $name)
        }
    }
    $swapped = $true
    Move-Item -LiteralPath (Join-Path $stagingRoot "python") -Destination (Join-Path $runtimeRoot "python")
    Move-Item -LiteralPath (Join-Path $stagingRoot "models") -Destination (Join-Path $runtimeRoot "models")
    Move-Item -LiteralPath (Join-Path $stagingRoot "runtime-manifest.json") -Destination $manifestPath

    & $verifyScript -RuntimeRoot $runtimeRoot -ConfigPath $ConfigPath | Out-Host
    Remove-GeneratedTree $backupRoot $runtimeRoot
    Remove-GeneratedTree $stagingRoot $runtimeRoot
    Write-Host "Portable runtime is ready for Forge packaging."
} catch {
    if ($swapped) {
        foreach ($name in @("python", "models", "runtime-manifest.json")) {
            $target = Join-Path $runtimeRoot $name
            if (Test-Path -LiteralPath $target) {
                if ((Get-Item -LiteralPath $target).PSIsContainer) {
                    Remove-GeneratedTree $target $runtimeRoot
                } else {
                    Remove-Item -LiteralPath (Assert-PathWithin $target $runtimeRoot) -Force
                }
            }
            $backup = Join-Path $backupRoot $name
            if (Test-Path -LiteralPath $backup) {
                Move-Item -LiteralPath (Assert-PathWithin $backup $runtimeRoot) -Destination $target
            }
        }
    }
    if (Test-Path -LiteralPath $stagingRoot) {
        Remove-GeneratedTree $stagingRoot $runtimeRoot
    }
    if (Test-Path -LiteralPath $backupRoot) {
        Remove-GeneratedTree $backupRoot $runtimeRoot
    }
    throw
} finally {
    if ($null -ne $buildMutex) {
        try {
            $buildMutex.ReleaseMutex()
        } catch [Threading.ApplicationException] {
            # The mutex was not owned (for example, initialization failed).
        }
        $buildMutex.Dispose()
    }
}
