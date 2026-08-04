[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-PathWithin([string]$Path, [string]$Parent) {
    $fullPath = [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $fullParent = [IO.Path]::GetFullPath($Parent).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $prefix = $fullParent + [IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to mutate a path outside $fullParent`: $fullPath"
    }
    return $fullPath
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
$finalOut = Join-Path $repoRoot "out"
$publicRoot = [Environment]::GetEnvironmentVariable("PUBLIC")
if ([string]::IsNullOrWhiteSpace($publicRoot)) {
    $publicRoot = [Environment]::GetFolderPath("CommonDocuments")
    if (-not [string]::IsNullOrWhiteSpace($publicRoot)) {
        $publicRoot = Split-Path -Parent $publicRoot
    }
}
if ([string]::IsNullOrWhiteSpace($publicRoot)) {
    throw "Could not locate the Windows Public profile for the ASCII Forge staging directory."
}
$publicRoot = [IO.Path]::GetFullPath($publicRoot)
$stageRoot = Join-Path $publicRoot ("ModelTrainingForge-" + [Guid]::NewGuid().ToString("N"))
$stageRoot = Assert-PathWithin $stageRoot $publicRoot
if ($stageRoot -match "[^\x00-\x7f]") {
    throw "Squirrel requires an ASCII-only staging path, but Windows Public resolved to: $stageRoot"
}
$stageOut = Join-Path $stageRoot "out"
$stageTemp = Join-Path $stageRoot "tmp"
$stagedPackage = Join-Path $stageOut "YOLO26ModelTraining-win32-x64"

$oldForgeOut = [Environment]::GetEnvironmentVariable("MT_FORGE_OUT_DIR", "Process")
$oldTemp = [Environment]::GetEnvironmentVariable("TEMP", "Process")
$oldTmp = [Environment]::GetEnvironmentVariable("TMP", "Process")

try {
    [IO.Directory]::CreateDirectory($stageOut) | Out-Null
    [IO.Directory]::CreateDirectory($stageTemp) | Out-Null
    $env:MT_FORGE_OUT_DIR = $stageOut
    $env:TEMP = $stageTemp
    $env:TMP = $stageTemp

    Write-Host "Building Windows x64 distributables through ASCII staging: $stageRoot"
    & npx.cmd electron-forge make --platform=win32 --arch=x64
    if ($LASTEXITCODE -ne 0) {
        throw "Electron Forge make failed (exit code $LASTEXITCODE)."
    }

    $verify = Join-Path $scriptRoot "verify-packaged-app.ps1"
    & $verify -PackageRoot $stagedPackage

    $zipFile = Get-ChildItem -LiteralPath (Join-Path $stageOut "make\zip\win32\x64") -File -Filter "*.zip" |
        Select-Object -First 1
    if ($null -eq $zipFile) {
        throw "Electron Forge did not create the Windows x64 portable ZIP."
    }
    $zipCheckRoot = Join-Path $stageRoot "z"
    [IO.Directory]::CreateDirectory($zipCheckRoot) | Out-Null
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::ExtractToDirectory($zipFile.FullName, $zipCheckRoot)
    $zipPackageRoot = $zipCheckRoot
    if (-not (Test-Path -LiteralPath (Join-Path $zipPackageRoot "resources\python\python.exe") -PathType Leaf)) {
        $zipPackageRoot = Get-ChildItem -LiteralPath $zipCheckRoot -Directory -Recurse |
            Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "resources\python\python.exe") -PathType Leaf } |
            Select-Object -First 1 -ExpandProperty FullName
    }
    if ([string]::IsNullOrWhiteSpace($zipPackageRoot)) {
        throw "The portable ZIP did not contain a packaged application root."
    }
    & $verify -PackageRoot $zipPackageRoot
    Write-Host "Portable ZIP fully extracted and verified: $($zipFile.FullName)"

    [IO.Directory]::CreateDirectory($finalOut) | Out-Null
    Copy-Item -LiteralPath $stagedPackage -Destination (Join-Path $finalOut "YOLO26ModelTraining-win32-x64") -Recurse
    Copy-Item -LiteralPath (Join-Path $stageOut "make") -Destination (Join-Path $finalOut "make") -Recurse

    & $verify -PackageRoot (Join-Path $finalOut "YOLO26ModelTraining-win32-x64")
    Write-Host "Windows x64 portable ZIP copied to: $(Join-Path $finalOut 'make')"
} finally {
    [Environment]::SetEnvironmentVariable("MT_FORGE_OUT_DIR", $oldForgeOut, "Process")
    [Environment]::SetEnvironmentVariable("TEMP", $oldTemp, "Process")
    [Environment]::SetEnvironmentVariable("TMP", $oldTmp, "Process")
    if (Test-Path -LiteralPath $stageRoot) {
        $safeStage = Assert-PathWithin $stageRoot $publicRoot
        Remove-Item -LiteralPath $safeStage -Recurse -Force
    }
}
