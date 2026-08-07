[CmdletBinding()]
param(
    [string]$RuntimeVersion = "1.0.0",
    [string]$ConfigPath,
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
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
    $ConfigPath = Join-Path $scriptRoot "runtime-win-x64-cuda.lock.json"
}
$ConfigPath = [IO.Path]::GetFullPath($ConfigPath)
if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
    throw "Runtime lock file not found: $ConfigPath"
}
$config = [IO.File]::ReadAllText($ConfigPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
$acceleratorProperty = $config.PSObject.Properties["accelerator"]
$flavor = if ($null -ne $acceleratorProperty -and -not [string]::IsNullOrWhiteSpace([string]$acceleratorProperty.Value.flavor)) {
    ([string]$acceleratorProperty.Value.flavor).ToLowerInvariant()
} elseif (([string]$config.runtimeId) -match "cpu") {
    "cpu"
} else {
    "cuda"
}
if ($flavor -notin @("cpu", "cuda")) {
    throw "Unsupported runtime accelerator flavor: $flavor"
}
if ([string]::IsNullOrWhiteSpace($RuntimeRepoDir)) {
    $RuntimeRepoDir = Join-Path (Split-Path -Parent $repoRoot) "YOLO26ModelTraining-Runtime"
}
$RuntimeRepoDir = [IO.Path]::GetFullPath($RuntimeRepoDir)
$tag = if ($flavor -eq "cpu") { "runtime-cpu-v$RuntimeVersion" } else { "runtime-v$RuntimeVersion" }
$manifestFileName = if ($flavor -eq "cpu") { "latest-cpu.json" } else { "latest.json" }
$releaseDir = Join-Path $repoRoot "artifacts\runtime-release\$tag"
$latest = Join-Path $releaseDir "latest.json"
$gh = Get-GitHubCli

if (-not $SkipBuild) {
    & (Join-Path $scriptRoot "build-runtime-release.ps1") -RuntimeVersion $RuntimeVersion -ConfigPath $ConfigPath
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

Push-Location $RuntimeRepoDir
try {
    $dirty = git status --porcelain
    if (-not [string]::IsNullOrWhiteSpace(($dirty -join ""))) {
        throw "The Runtime feed repository has uncommitted changes."
    }

    $releaseTags = @(& $gh release list --repo GXICll-Dev/YOLO26ModelTraining-Runtime --limit 100 --json tagName --jq '.[].tagName')
    if ($releaseTags -notcontains $tag) {
        $title = if ($flavor -eq "cpu") { "CPU Runtime v$RuntimeVersion - Python 3.11 / PyTorch CPU" } else { "Runtime v$RuntimeVersion - Python 3.11 / PyTorch CUDA 12.6" }
        & $gh release create $tag --repo GXICll-Dev/YOLO26ModelTraining-Runtime --draft --title $title --notes "Managed runtime for YOLO26ModelTraining. Every asset is SHA-256 verified before atomic deployment."
        if ($LASTEXITCODE -ne 0) { throw "Could not create runtime release $tag." }
    } else {
        $existingRelease = (& $gh release view $tag --repo GXICll-Dev/YOLO26ModelTraining-Runtime --json isDraft) | ConvertFrom-Json
        if (-not [bool]$existingRelease.isDraft) {
            throw "Runtime release $tag is already public and immutable."
        }
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

    $remoteRelease = (& $gh release view $tag --repo GXICll-Dev/YOLO26ModelTraining-Runtime --json isDraft,assets) | ConvertFrom-Json
    foreach ($asset in $assets) {
        $remoteAsset = @($remoteRelease.assets | Where-Object { $_.name -eq $asset.Name }) | Select-Object -First 1
        if ($null -eq $remoteAsset) {
            throw "Uploaded Runtime asset is missing: $($asset.Name)"
        }
        if ([long]$remoteAsset.size -ne [long]$asset.Length) {
            throw "Uploaded Runtime asset size mismatch: $($asset.Name)"
        }
        $expectedDigest = "sha256:" + (Get-FileHash -LiteralPath $asset.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        if (-not [string]::IsNullOrWhiteSpace([string]$remoteAsset.digest) -and [string]$remoteAsset.digest -ne $expectedDigest) {
            throw "Uploaded Runtime asset digest mismatch: $($asset.Name)"
        }
    }
    if ([bool]$remoteRelease.isDraft) {
        & $gh release edit $tag --repo GXICll-Dev/YOLO26ModelTraining-Runtime --draft=false
        if ($LASTEXITCODE -ne 0) { throw "Could not publish Runtime release $tag." }
    }

    Copy-Item -LiteralPath $latest -Destination (Join-Path $runtimeManifestDir $manifestFileName) -Force
    git add README.md .gitignore ("runtime/" + $manifestFileName)
    $changes = git diff --cached --name-only
    if (-not [string]::IsNullOrWhiteSpace(($changes -join ""))) {
        git commit -m "Publish $flavor runtime v$RuntimeVersion manifest"
        if ($LASTEXITCODE -ne 0) { throw "Could not commit runtime manifest." }
    }
    git push origin main
    if ($LASTEXITCODE -ne 0) { throw "Could not push runtime manifest." }
    Write-Host "Published runtime release $tag."
} finally {
    Pop-Location
}
