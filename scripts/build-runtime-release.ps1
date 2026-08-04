[CmdletBinding()]
param(
    [string]$RuntimeVersion = "1.0.0",
    [string]$OutputDir,
    [long]$MaxPackageBytes = 1400MB,
    [string]$ReleaseRepository = "GXICll-Dev/YOLO26ModelTraining-Runtime"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-PathWithin([string]$Path, [string]$Parent) {
    $fullPath = [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $fullParent = [IO.Path]::GetFullPath($Parent).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    if (-not $fullPath.StartsWith($fullParent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to write outside $fullParent`: $fullPath"
    }
    return $fullPath
}

function Write-Utf8Json([object]$Value, [string]$Path) {
    $json = $Value | ConvertTo-Json -Depth 12
    [IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

function New-Archive([string]$ArchivePath, [object[]]$Files) {
    if ($Files.Count -eq 0) {
        throw "Cannot create an empty runtime archive: $ArchivePath"
    }
    if (Test-Path -LiteralPath $ArchivePath) {
        Remove-Item -LiteralPath $ArchivePath -Force
    }
    $stream = [IO.File]::Open($ArchivePath, [IO.FileMode]::CreateNew, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    try {
        $archive = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create, $false)
        try {
            foreach ($file in $Files) {
                $entryName = $file.RelativePath.Replace("\", "/")
                [IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                    $archive,
                    $file.SourcePath,
                    $entryName,
                    [IO.Compression.CompressionLevel]::Optimal
                ) | Out-Null
            }
        } finally {
            $archive.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

function Get-MappedFiles([string]$SourceRoot, [string]$TargetPrefix = "") {
    $sourceRootFull = [IO.Path]::GetFullPath($SourceRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    @(Get-ChildItem -LiteralPath $sourceRootFull -File -Recurse -Force | ForEach-Object {
        $relative = $_.FullName.Substring($sourceRootFull.Length).TrimStart([IO.Path]::DirectorySeparatorChar)
        if (-not [string]::IsNullOrWhiteSpace($TargetPrefix)) {
            $relative = Join-Path $TargetPrefix $relative
        }
        [PSCustomObject]@{
            SourcePath = $_.FullName
            RelativePath = $relative
            Length = [long]$_.Length
        }
    })
}

function Split-Files([object[]]$Files, [long]$MaximumBytes) {
    $groups = [Collections.Generic.List[object]]::new()
    $current = [Collections.Generic.List[object]]::new()
    [long]$currentBytes = 0
    foreach ($file in @($Files | Sort-Object SourcePath)) {
        if ($file.Length -gt $MaximumBytes) {
            throw "A single runtime file exceeds the package limit: $($file.SourcePath) ($($file.Length) bytes)"
        }
        if ($current.Count -gt 0 -and ($currentBytes + $file.Length) -gt $MaximumBytes) {
            $groups.Add(@($current))
            $current = [Collections.Generic.List[object]]::new()
            $currentBytes = 0
        }
        $current.Add($file)
        $currentBytes += $file.Length
    }
    if ($current.Count -gt 0) {
        $groups.Add(@($current))
    }
    return @($groups)
}

function New-PackageRecord([string]$Name, [string]$FileName, [object[]]$Files) {
    $path = Join-Path $OutputDir $FileName
    Write-Host "Creating $FileName from $($Files.Count) files..."
    New-Archive $path $Files
    $info = Get-Item -LiteralPath $path
    if ($info.Length -ge 2GB) {
        throw "GitHub runtime asset is too large: $FileName ($($info.Length) bytes)"
    }
    $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    [PSCustomObject]@{
        name = $Name
        fileName = $FileName
        url = "https://github.com/$ReleaseRepository/releases/download/runtime-v$RuntimeVersion/$FileName"
        size = [long]$info.Length
        sha256 = $hash
    }
}

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $repoRoot "artifacts\runtime-release\runtime-v$RuntimeVersion"
}
$OutputDir = Assert-PathWithin $OutputDir $repoRoot
[IO.Directory]::CreateDirectory($OutputDir) | Out-Null

$runtimeRoot = Join-Path $repoRoot "runtime"
$pythonRoot = Join-Path $runtimeRoot "python"
$torchRoot = Join-Path $pythonRoot "Lib\site-packages\torch"
$modelsRoot = Join-Path $runtimeRoot "models"
$toolsRoot = Join-Path $repoRoot "tools\labelme"
$runtimeManifest = Join-Path $runtimeRoot "runtime-manifest.json"

foreach ($required in @($pythonRoot, $torchRoot, $modelsRoot, $toolsRoot, $runtimeManifest)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "Runtime source is missing: $required"
    }
}

& (Join-Path $scriptRoot "verify-portable-runtime.ps1") -RuntimeRoot $runtimeRoot

$allPython = Get-MappedFiles $pythonRoot "python"
$torchPrefix = ([IO.Path]::GetFullPath($torchRoot).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar)
$torchFiles = @($allPython | Where-Object { $_.SourcePath.StartsWith($torchPrefix, [StringComparison]::OrdinalIgnoreCase) })
$pythonBaseFiles = @($allPython | Where-Object { -not $_.SourcePath.StartsWith($torchPrefix, [StringComparison]::OrdinalIgnoreCase) })
$toolFiles = Get-MappedFiles $toolsRoot "tools\labelme"
$modelFiles = @(Get-MappedFiles $modelsRoot "models")
$modelFiles += [PSCustomObject]@{ SourcePath = $runtimeManifest; RelativePath = "runtime-manifest.json"; Length = (Get-Item -LiteralPath $runtimeManifest).Length }

$packages = [Collections.Generic.List[object]]::new()
$packages.Add((New-PackageRecord "Python base runtime" "runtime-python-base-v$RuntimeVersion.zip" $pythonBaseFiles))

$torchGroups = Split-Files $torchFiles $MaxPackageBytes
for ($index = 0; $index -lt $torchGroups.Count; $index++) {
    $number = ($index + 1).ToString("00")
    $packages.Add((New-PackageRecord "PyTorch CUDA package $($index + 1)/$($torchGroups.Count)" "runtime-torch-$number-v$RuntimeVersion.zip" @($torchGroups[$index])))
}

$packages.Add((New-PackageRecord "LabelMe tools" "runtime-tools-v$RuntimeVersion.zip" $toolFiles))
$packages.Add((New-PackageRecord "YOLO26 models and manifest" "runtime-models-v$RuntimeVersion.zip" $modelFiles))

$requiredRelativePaths = @(
    "python/python.exe",
    "python/python311.dll",
    "python/MSVCP140.dll",
    "python/CONCRT140.dll",
    "python/Lib/site-packages/torch/__init__.py",
    "python/Lib/site-packages/torch/lib/torch_cuda.dll",
    "python/Lib/site-packages/ultralytics/__init__.py",
    "models/yolo26n.pt",
    "tools/labelme/labelme.exe",
    "runtime-manifest.json"
)
$requiredFiles = foreach ($relative in $requiredRelativePaths) {
    $source = if ($relative.StartsWith("tools/labelme/")) {
        Join-Path $repoRoot $relative.Replace("/", "\")
    } else {
        Join-Path $runtimeRoot $relative.Replace("/", "\")
    }
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "Required runtime file is missing: $source"
    }
    $info = Get-Item -LiteralPath $source
    [PSCustomObject]@{
        path = $relative
        size = [long]$info.Length
        sha256 = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

$installedSize = ($allPython + $toolFiles + $modelFiles | Measure-Object -Property Length -Sum).Sum
$release = [ordered]@{
    schemaVersion = 1
    runtimeId = "windows-x64-cuda126-py311"
    runtimeVersion = $RuntimeVersion
    displayName = "Windows x64 / Python 3.11 / PyTorch CUDA 12.6"
    publishedAt = [DateTime]::UtcNow.ToString("o")
    installedSize = [long]$installedSize
    packages = @($packages)
    requiredFiles = @($requiredFiles)
}

$releasePath = Join-Path $OutputDir "runtime-release.json"
$latestPath = Join-Path $OutputDir "latest.json"
Write-Utf8Json $release $releasePath
Write-Utf8Json $release $latestPath

$downloadBytes = ($packages | Measure-Object -Property size -Sum).Sum
Write-Host "Runtime release prepared."
Write-Host "Output: $OutputDir"
Write-Host ("Packages: {0}" -f $packages.Count)
Write-Host ("Download size: {0:N2} GiB" -f ($downloadBytes / 1GB))
Write-Host ("Installed size: {0:N2} GiB" -f ($installedSize / 1GB))
