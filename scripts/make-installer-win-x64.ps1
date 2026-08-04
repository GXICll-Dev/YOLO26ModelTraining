[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-PathWithin([string]$Path, [string]$Parent) {
    $fullPath = [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $fullParent = [IO.Path]::GetFullPath($Parent).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    if (-not $fullPath.StartsWith($fullParent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to mutate a path outside $fullParent`: $fullPath"
    }
    return $fullPath
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
$package = Get-Content -LiteralPath (Join-Path $repoRoot "package.json") -Raw -Encoding UTF8 | ConvertFrom-Json
$version = [string]$package.version
$publicRoot = [Environment]::GetEnvironmentVariable("PUBLIC")
if ([string]::IsNullOrWhiteSpace($publicRoot)) {
    $publicRoot = [Environment]::GetFolderPath("CommonDocuments")
    if (-not [string]::IsNullOrWhiteSpace($publicRoot)) {
        $publicRoot = Split-Path -Parent $publicRoot
    }
}
if ([string]::IsNullOrWhiteSpace($publicRoot)) {
    throw "Could not locate an ASCII-only Windows staging directory."
}
$publicRoot = [IO.Path]::GetFullPath($publicRoot)
$stageRoot = Assert-PathWithin (Join-Path $publicRoot ("ModelTrainingInstaller-" + [Guid]::NewGuid().ToString("N"))) $publicRoot
$stageOut = Join-Path $stageRoot "out"
$stageTemp = Join-Path $stageRoot "tmp"
$stagedPackage = Join-Path $stageOut "YOLO26ModelTraining-win32-x64"
$finalRoot = Join-Path $repoRoot "out\installer\win32\x64"
$finalRoot = Assert-PathWithin $finalRoot $repoRoot

$oldForgeOut = [Environment]::GetEnvironmentVariable("MT_FORGE_OUT_DIR", "Process")
$oldTemp = [Environment]::GetEnvironmentVariable("TEMP", "Process")
$oldTmp = [Environment]::GetEnvironmentVariable("TMP", "Process")
$oldCoreOnly = [Environment]::GetEnvironmentVariable("MT_CORE_ONLY", "Process")
$oldSquirrel = [Environment]::GetEnvironmentVariable("MT_ENABLE_SQUIRREL", "Process")

try {
    [IO.Directory]::CreateDirectory($stageOut) | Out-Null
    [IO.Directory]::CreateDirectory($stageTemp) | Out-Null
    $env:MT_FORGE_OUT_DIR = $stageOut
    $env:TEMP = $stageTemp
    $env:TMP = $stageTemp
    $env:MT_CORE_ONLY = "1"
    $env:MT_ENABLE_SQUIRREL = "1"

    & npx.cmd electron-forge make --platform=win32 --arch=x64
    if ($LASTEXITCODE -ne 0) {
        throw "Electron Forge installer build failed (exit code $LASTEXITCODE)."
    }

    & (Join-Path $scriptRoot "verify-core-package.ps1") -PackageRoot $stagedPackage

    $setup = Get-ChildItem -LiteralPath (Join-Path $stageOut "make") -File -Recurse -Filter "*.exe" |
        Where-Object { $_.Name -match "Setup" } |
        Select-Object -First 1
    if ($null -eq $setup) {
        throw "Squirrel did not create a Windows Setup executable."
    }

    if (Test-Path -LiteralPath $finalRoot) {
        $safeFinal = Assert-PathWithin $finalRoot $repoRoot
        Remove-Item -LiteralPath $safeFinal -Recurse -Force
    }
    [IO.Directory]::CreateDirectory($finalRoot) | Out-Null
    $installerName = "YOLO26ModelTraining-Setup-$version.exe"
    $installerPath = Join-Path $finalRoot $installerName
    Copy-Item -LiteralPath $setup.FullName -Destination $installerPath
    Copy-Item -LiteralPath (Join-Path $stageOut "make") -Destination (Join-Path $finalRoot "squirrel") -Recurse

    $hash = (Get-FileHash -LiteralPath $installerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$installerPath.sha256", "$hash  $installerName`n", [Text.UTF8Encoding]::new($false))
    Write-Host "Installer created: $installerPath"
    Write-Host "SHA-256: $hash"
} finally {
    [Environment]::SetEnvironmentVariable("MT_FORGE_OUT_DIR", $oldForgeOut, "Process")
    [Environment]::SetEnvironmentVariable("TEMP", $oldTemp, "Process")
    [Environment]::SetEnvironmentVariable("TMP", $oldTmp, "Process")
    [Environment]::SetEnvironmentVariable("MT_CORE_ONLY", $oldCoreOnly, "Process")
    [Environment]::SetEnvironmentVariable("MT_ENABLE_SQUIRREL", $oldSquirrel, "Process")
    if (Test-Path -LiteralPath $stageRoot) {
        $safeStage = Assert-PathWithin $stageRoot $publicRoot
        Remove-Item -LiteralPath $safeStage -Recurse -Force
    }
}
