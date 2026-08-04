[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
$outputDir = Join-Path $repoRoot "bin"
$output = Join-Path $outputDir "modeltraining-server.exe"
$uninstaller = Join-Path $outputDir "YOLO26ModelTraining-Uninstall.exe"
[IO.Directory]::CreateDirectory($outputDir) | Out-Null

$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$oldCGOEnabled = $env:CGO_ENABLED
try {
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    & go build -trimpath -o $output ./cmd/server
    if ($LASTEXITCODE -ne 0) {
        throw "Building the Windows x64 Go backend failed (exit code $LASTEXITCODE)."
    }
    & go build -trimpath -ldflags "-H=windowsgui -s -w" -o $uninstaller ./cmd/uninstaller
    if ($LASTEXITCODE -ne 0) {
        throw "Building the Windows x64 uninstaller failed (exit code $LASTEXITCODE)."
    }
} finally {
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:CGO_ENABLED = $oldCGOEnabled
}

if (-not (Test-Path -LiteralPath $output -PathType Leaf)) {
    throw "The Windows x64 backend was not created: $output"
}
if (-not (Test-Path -LiteralPath $uninstaller -PathType Leaf)) {
    throw "The Windows x64 uninstaller was not created: $uninstaller"
}
Write-Host "Windows x64 backend built: $output"
Write-Host "Windows x64 uninstaller built: $uninstaller"
