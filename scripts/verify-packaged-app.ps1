[CmdletBinding()]
param(
    [string]$PackageRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
if ([string]::IsNullOrWhiteSpace($PackageRoot)) {
    $PackageRoot = Join-Path $repoRoot "out\YOLO26ModelTraining-win32-x64"
}
$PackageRoot = [IO.Path]::GetFullPath($PackageRoot)
$resources = Join-Path $PackageRoot "resources"

$required = @(
    (Join-Path $PackageRoot "YOLO26ModelTraining.exe"),
    (Join-Path $resources "app.asar"),
    (Join-Path $resources "bin\modeltraining-server.exe"),
    (Join-Path $resources "dist\index.html"),
    (Join-Path $resources "python\python.exe"),
    (Join-Path $resources "python\MSVCP140.dll"),
    (Join-Path $resources "python\CONCRT140.dll"),
    (Join-Path $resources "models\yolo26n.pt"),
    (Join-Path $resources "tools\labelme\labelme.exe"),
    (Join-Path $resources "tools\labelme\_internal\CONCRT140.dll"),
    (Join-Path $resources "runtime-manifest.json")
)
foreach ($path in $required) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Packaged application resource is missing: $path"
    }
}

$pythonConcrt = Join-Path $resources "python\CONCRT140.dll"
$labelMeConcrt = Join-Path $resources "tools\labelme\_internal\CONCRT140.dll"
if ((Get-FileHash -LiteralPath $pythonConcrt -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $labelMeConcrt -Algorithm SHA256).Hash) {
    throw "Packaged LabelMe CONCRT140.dll does not match the pinned Python runtime copy."
}

$pythonCacheDirectories = @(Get-ChildItem -LiteralPath $resources -Directory -Filter "__pycache__" -Recurse -Force -ErrorAction SilentlyContinue)
$pythonBytecodeFiles = @(
    Get-ChildItem -LiteralPath $resources -File -Recurse -Force -ErrorAction SilentlyContinue |
        Where-Object { $_.Extension -in @(".pyc", ".pyo") }
)
if ($pythonCacheDirectories.Count -gt 0 -or $pythonBytecodeFiles.Count -gt 0) {
    $example = @($pythonCacheDirectories.FullName) + @($pythonBytecodeFiles.FullName) | Select-Object -First 1
    throw "Packaged application contains generated Python bytecode/cache: $example"
}

$verifyRuntime = Join-Path $scriptRoot "verify-portable-runtime.ps1"
& $verifyRuntime -RuntimeRoot $resources
if ($LASTEXITCODE -ne 0) {
    throw "The packaged portable runtime failed verification (exit code $LASTEXITCODE)."
}

$packageBytes = (Get-ChildItem -LiteralPath $PackageRoot -File -Recurse -Force | Measure-Object -Property Length -Sum).Sum
Write-Host "Packaged Windows x64 application verified."
Write-Host ("Package root: {0}" -f $PackageRoot)
Write-Host ("Unpacked size: {0:N1} MiB" -f ($packageBytes / 1MB))
