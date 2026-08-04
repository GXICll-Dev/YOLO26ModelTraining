[CmdletBinding()]
param(
    [string]$RuntimeVersion = "1.0.0",
    [string]$RuntimeRepoDir,
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$env:GODEBUG = "http2client=0"

function Get-GitHubCli {
    $command = Get-Command gh -ErrorAction SilentlyContinue
    if ($null -ne $command) { return $command.Source }
    $fallback = "C:\Program Files\GitHub CLI\gh.exe"
    if (Test-Path -LiteralPath $fallback -PathType Leaf) { return $fallback }
    throw "GitHub CLI is not installed."
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptRoot ".."))
if ([string]::IsNullOrWhiteSpace($RuntimeRepoDir)) {
    $RuntimeRepoDir = Join-Path (Split-Path -Parent $repoRoot) "YOLO26ModelTraining-Runtime"
}
$RuntimeRepoDir = [IO.Path]::GetFullPath($RuntimeRepoDir)
$releaseDir = Join-Path $repoRoot "artifacts\runtime-release\runtime-v$RuntimeVersion"
$latest = Join-Path $releaseDir "latest.json"
$tag = "runtime-v$RuntimeVersion"
$gh = Get-GitHubCli

if (-not $SkipBuild) {
    & (Join-Path $scriptRoot "build-runtime-release.ps1") -RuntimeVersion $RuntimeVersion
    if ($LASTEXITCODE -ne 0) { throw "Runtime release build failed." }
    & node.exe (Join-Path $repoRoot "electron\verify-runtime-release.cjs") (Join-Path $releaseDir "runtime-release.json")
    if ($LASTEXITCODE -ne 0) { throw "Runtime release verification failed." }
}
if (-not (Test-Path -LiteralPath (Join-Path $RuntimeRepoDir ".git"))) {
    throw "Runtime repository is not cloned: $RuntimeRepoDir"
}
if (-not (Test-Path -LiteralPath $latest -PathType Leaf)) {
    throw "Runtime latest.json is missing: $latest"
}

$runtimeManifestDir = Join-Path $RuntimeRepoDir "runtime"
[IO.Directory]::CreateDirectory($runtimeManifestDir) | Out-Null
Copy-Item -LiteralPath $latest -Destination (Join-Path $runtimeManifestDir "latest.json") -Force

Push-Location $RuntimeRepoDir
try {
    git add README.md .gitignore runtime/latest.json
    $changes = git diff --cached --name-only
    if (-not [string]::IsNullOrWhiteSpace(($changes -join ""))) {
        git commit -m "Publish runtime v$RuntimeVersion manifest"
    }
    git push origin main
    if ($LASTEXITCODE -ne 0) { throw "Could not push runtime manifest." }

    $releaseTags = @(& $gh release list --repo GXICll-Dev/YOLO26ModelTraining-Runtime --limit 100 --json tagName | ConvertFrom-Json | ForEach-Object { $_.tagName })
    if ($releaseTags -notcontains $tag) {
        & $gh release create $tag --repo GXICll-Dev/YOLO26ModelTraining-Runtime --title "Runtime v$RuntimeVersion - Python 3.11 / PyTorch CUDA 12.6" --notes "Managed runtime for YOLO26ModelTraining. Every asset is SHA-256 verified before atomic deployment."
        if ($LASTEXITCODE -ne 0) { throw "Could not create runtime release $tag." }
    }

    $assets = @(Get-ChildItem -LiteralPath $releaseDir -File | Where-Object { $_.Extension -eq ".zip" -or $_.Name -eq "runtime-release.json" } | Sort-Object Length)
    foreach ($asset in $assets) {
        $uploaded = $false
        for ($attempt = 1; $attempt -le 4; $attempt++) {
            Write-Host "Uploading $($asset.Name) (attempt $attempt/4)..."
            & $gh release upload $tag $asset.FullName --repo GXICll-Dev/YOLO26ModelTraining-Runtime --clobber
            if ($LASTEXITCODE -eq 0) {
                $uploaded = $true
                break
            }
            Start-Sleep -Seconds 5
        }
        if (-not $uploaded) {
            throw "Could not upload $($asset.Name)."
        }
    }
    Write-Host "Published runtime release $tag."
} finally {
    Pop-Location
}
