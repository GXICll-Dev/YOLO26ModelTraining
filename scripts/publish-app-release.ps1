[CmdletBinding()]
param(
    [string]$NotesFile,
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$env:GODEBUG = "http2client=0"

function Get-GitHubCli {
    $command = Get-Command gh -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }
    $fallback = "C:\Program Files\GitHub CLI\gh.exe"
    if (Test-Path -LiteralPath $fallback -PathType Leaf) {
        return $fallback
    }
    throw "GitHub CLI is not installed. Install GitHub.cli with winget first."
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
$package = Get-Content -LiteralPath (Join-Path $repoRoot "package.json") -Raw -Encoding UTF8 | ConvertFrom-Json
$version = [string]$package.version
$tag = "v$version"
$installerDir = Join-Path $repoRoot "out\installer\win32\x64"
$installer = Join-Path $installerDir "YOLO26ModelTraining-Setup-$version.exe"
$checksum = "$installer.sha256"
if ([string]::IsNullOrWhiteSpace($NotesFile)) {
    $NotesFile = Join-Path $repoRoot "RELEASE_NOTES.md"
}
$NotesFile = [IO.Path]::GetFullPath($NotesFile)
$gh = Get-GitHubCli

Push-Location $repoRoot
try {
    & $gh auth status | Out-Host
    if ($LASTEXITCODE -ne 0) {
        throw "GitHub CLI is not authenticated. Run: gh auth login"
    }
    if (-not $SkipBuild) {
        & npm.cmd run electron:installer
        if ($LASTEXITCODE -ne 0) {
            throw "Installer build failed."
        }
    }
    foreach ($path in @($installer, $checksum, $NotesFile)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Release input is missing: $path"
        }
    }
    $dirty = git status --porcelain
    if (-not [string]::IsNullOrWhiteSpace(($dirty -join ""))) {
        throw "The source repository has uncommitted changes. Commit them before publishing."
    }
    git push origin main
    if ($LASTEXITCODE -ne 0) {
        throw "Could not push main."
    }
    & $gh release view $tag --repo GXICll-Dev/YOLO26ModelTraining *> $null
    if ($LASTEXITCODE -eq 0) {
        & $gh release edit $tag --repo GXICll-Dev/YOLO26ModelTraining --title "YOLO26ModelTraining v$version" --notes-file $NotesFile
    } else {
        & $gh release create $tag --repo GXICll-Dev/YOLO26ModelTraining --title "YOLO26ModelTraining v$version" --notes-file $NotesFile
    }
    if ($LASTEXITCODE -ne 0) {
        throw "Could not create or update GitHub Release $tag."
    }
    & $gh release upload $tag $installer $checksum --repo GXICll-Dev/YOLO26ModelTraining --clobber
    if ($LASTEXITCODE -ne 0) {
        throw "Could not upload app release assets."
    }
    Write-Host "Published application release $tag."
} finally {
    Pop-Location
}
