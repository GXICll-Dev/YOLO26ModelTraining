[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$PackageRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$PackageRoot = [IO.Path]::GetFullPath($PackageRoot)
$resources = Join-Path $PackageRoot "resources"
$required = @(
    (Join-Path $PackageRoot "YOLO26ModelTraining.exe"),
    (Join-Path $resources "app.asar"),
    (Join-Path $resources "bin\modeltraining-server.exe"),
    (Join-Path $resources "bin\YOLO26ModelTraining-Uninstall.exe"),
    (Join-Path $resources "dist\index.html")
)
foreach ($path in $required) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Core package resource is missing: $path"
    }
}

$forbidden = @(
    (Join-Path $resources "python"),
    (Join-Path $resources "tools\labelme"),
    (Join-Path $resources "models")
)
foreach ($path in $forbidden) {
    if (Test-Path -LiteralPath $path) {
        throw "Core installer unexpectedly contains the separately downloadable runtime: $path"
    }
}

$packageBytes = (Get-ChildItem -LiteralPath $PackageRoot -File -Recurse -Force | Measure-Object -Property Length -Sum).Sum
Write-Host "Core Windows package verified."
Write-Host "Package root: $PackageRoot"
Write-Host ("Unpacked size: {0:N1} MiB" -f ($packageBytes / 1MB))
