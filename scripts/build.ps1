<#
.SYNOPSIS
    Cross-platform build script for kocort (Windows PowerShell)

.DESCRIPTION
    Builds the kocort binary. No CGO or C/C++ compiler required.
    llama.cpp shared libraries are loaded at runtime via purego.
    Mirrors the functionality of scripts/build.sh for Windows.

.PARAMETER Target
    Build target(s). Comma-separated. Valid values:
    build       - Build the default kocort binary (CGO_ENABLED=0)
    all         - Build with extra tags from kocort_BUILD_TAGS
    test        - Run tests
    vet         - Run go vet
    clean       - Clean build artifacts
    cross       - Cross-compile for win-amd64 and win-arm64

.EXAMPLE
    # Default build
    .\scripts\build.ps1

    # Build + run tests
    .\scripts\build.ps1 build,test

    # Cross-compile for Windows amd64 and arm64
    .\scripts\build.ps1 cross

    # Clean
    .\scripts\build.ps1 clean
#>

param(
    [Parameter(Position = 0)]
    [string]$Target = "build"
)

$ErrorActionPreference = "Stop"

# ---------- paths ----------
$ScriptDir   = Split-Path -Parent $MyInvocation.MyCommand.Definition
$ProjectRoot = Split-Path -Parent $ScriptDir
$DistDir     = Join-Path $ProjectRoot "dist"
$WebDir      = Join-Path $ProjectRoot "web"
$EmbedDir    = Join-Path $ProjectRoot "api/static/dist"

# ---------- version ----------
$Version = $env:kocort_VERSION
if (-not $Version) {
    try {
        $Version = (git -C $ProjectRoot describe --tags --always --dirty 2>$null)
    } catch {}
    if (-not $Version) { $Version = "dev" }
}

$Commit = "unknown"
try {
    $Commit = (git -C $ProjectRoot rev-parse --short HEAD 2>$null)
} catch {}

$BuildDate = (Get-Date -Format "yyyy-MM-ddTHH:mm:ssZ")

# ---------- helpers ----------
function Write-Info  { param($msg) Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok    { param($msg) Write-Host "==> $msg" -ForegroundColor Green }
function Write-Warn  { param($msg) Write-Host "WARNING: $msg" -ForegroundColor Yellow }
function Write-Fail  { param($msg) Write-Host "ERROR: $msg" -ForegroundColor Red; exit 1 }

function Get-GoArch {
    if ($env:GOARCH) {
        return $env:GOARCH.ToLower()
    }
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        "x86"   { return "386" }
        default { return $arch.ToLower() }
    }
}

function Get-LdFlags {
    return "-s -w -X=kocort/version.Version=$Version -X=kocort/version.Commit=$Commit -X=kocort/version.BuildDate=$BuildDate"
}

function Invoke-WebBuild {
    if ($env:kocort_SKIP_WEB -eq "1") {
        Write-Warn "Skipping web build because kocort_SKIP_WEB=1"
        return
    }
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
        Write-Fail "npm is required to build the embedded web UI."
    }
    if (-not (Test-Path (Join-Path $WebDir "node_modules"))) {
        Write-Fail "web\node_modules is missing. Run 'npm install' in web\ first."
    }

    Write-Info "Building web UI for embed"
    Push-Location $WebDir
    try {
        & npm run build
        if ($LASTEXITCODE -ne 0) { Write-Fail "npm run build failed" }
    } finally {
        Pop-Location
    }

    $webOutDir = Join-Path $WebDir "out"
    if (-not (Test-Path $webOutDir)) {
        Write-Fail "web build finished but web\out was not generated"
    }

    if (-not (Test-Path $EmbedDir)) {
        New-Item -ItemType Directory -Path $EmbedDir -Force | Out-Null
    }
    Get-ChildItem $EmbedDir -Force | Where-Object { $_.Name -ne ".gitkeep" } | Remove-Item -Recurse -Force
    Copy-Item (Join-Path $webOutDir "*") $EmbedDir -Recurse -Force
    Write-Ok "Embedded web assets refreshed: $EmbedDir"
}

function Get-OutputName {
    param([string]$Tags, [string]$TargetOS = "windows")
    $suffix = if ($TargetOS -eq "windows") { ".exe" } else { "" }
    return "kocort$suffix"
}

# ---------- build ----------
function Invoke-Build {
    param(
        [string]$Tags = "",
        [string]$TargetOS   = "windows",
        [string]$TargetArch = ""
    )

    if (-not $TargetArch) {
        $TargetArch = Get-GoArch
    }

    if ($TargetOS -eq "windows" -and $TargetArch -eq (Get-GoArch)) {
        Invoke-WebBuild
    } else {
        Write-Warn "Skipping web rebuild for cross target ${TargetOS}/${TargetArch}; using existing embedded assets"
    }

    $env:CGO_ENABLED = "0"

    $outName = Get-OutputName -Tags $Tags -TargetOS $TargetOS
    $outDir  = if ($env:kocort_OUTPUT) {
        Split-Path -Parent $env:kocort_OUTPUT
    } else {
        Join-Path $DistDir "${TargetOS}_${TargetArch}"
    }
    $outPath = if ($env:kocort_OUTPUT) {
        $env:kocort_OUTPUT
    } else {
        Join-Path $outDir $outName
    }

    if (-not (Test-Path $outDir)) {
        New-Item -ItemType Directory -Path $outDir -Force | Out-Null
    }

    $ldflags = Get-LdFlags

    $buildArgs = @("-trimpath", "-ldflags", $ldflags, "-o", $outPath)
    if ($Tags) {
        $buildArgs += @("-tags", $Tags)
    }

    $parallel = if ($env:kocort_PARALLEL) { $env:kocort_PARALLEL } else {
        (Get-CimInstance -ClassName Win32_Processor -ErrorAction SilentlyContinue |
            Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum
    }
    if ($parallel) {
        $buildArgs += @("-p", "$parallel")
    }

    Write-Info "Building kocort"
    Write-Info "  GOOS=$TargetOS GOARCH=$TargetArch"
    Write-Info "  CGO_ENABLED=0 (llama.cpp loaded via purego at runtime)"
    Write-Info "  Tags: $(if ($Tags) { $Tags } else { '<none>' })"
    Write-Info "  Output: $outPath"
    Write-Host ""

    Push-Location $ProjectRoot
    try {
        $env:GOOS   = $TargetOS
        $env:GOARCH = $TargetArch
        & go build @buildArgs ./cmd/kocort
        if ($LASTEXITCODE -ne 0) { Write-Fail "go build failed with exit code $LASTEXITCODE" }
    } finally {
        # Restore GOOS/GOARCH
        $env:GOOS   = ""
        $env:GOARCH = ""
        Pop-Location
    }

    Write-Ok "Build successful: $outPath"
    Write-Host ""
    Write-Info "Runtime: set KOCORT_LLAMA_LIB_DIR to use a local llama.cpp library directory,"
    Write-Info "         or libraries will be downloaded on first use."
}

# ---------- test ----------
function Invoke-Test {
    param([string]$Tags = "")

    $env:CGO_ENABLED = "0"
    Write-Info "Running tests$(if ($Tags) { " (tags: $Tags)" })"
    Push-Location $ProjectRoot
    try {
        Write-Info "=== llamadl package tests ==="
        $testArgs = @("-v", "-count=1", "./internal/llamadl/...")
        if ($Tags) { $testArgs = @("-tags", $Tags) + $testArgs }
        & go test @testArgs
        if ($LASTEXITCODE -ne 0) { Write-Fail "llamadl tests failed" }

        Write-Info "=== cerebellum package tests ==="
        $testArgs = @("-v", "-count=1", "./internal/cerebellum/...")
        if ($Tags) { $testArgs = @("-tags", $Tags) + $testArgs }
        & go test @testArgs
        if ($LASTEXITCODE -ne 0) { Write-Fail "cerebellum tests failed" }

        Write-Info "=== all package tests ==="
        $testArgs = @("-count=1", "-timeout", "120s", "./...")
        if ($Tags) { $testArgs = @("-tags", $Tags) + $testArgs }
        & go test @testArgs
        if ($LASTEXITCODE -ne 0) { Write-Fail "tests failed" }
    } finally {
        Pop-Location
    }
    Write-Ok "All tests passed"
}

# ---------- vet ----------
function Invoke-Vet {
    param([string]$Tags = "")

    $env:CGO_ENABLED = "0"
    Write-Info "Running go vet$(if ($Tags) { " (tags: $Tags)" })"
    Push-Location $ProjectRoot
    try {
        $vetArgs = @("./...")
        if ($Tags) { $vetArgs = @("-tags", $Tags) + $vetArgs }
        & go vet @vetArgs
        if ($LASTEXITCODE -ne 0) { Write-Fail "go vet failed" }
    } finally {
        Pop-Location
    }
    Write-Ok "go vet passed"
}

# ---------- clean ----------
function Invoke-Clean {
    Write-Info "Cleaning build cache..."
    & go clean -cache
    if (Test-Path $DistDir) {
        Remove-Item -Recurse -Force $DistDir
        Write-Info "Removed $DistDir"
    }
    if (Test-Path $EmbedDir) {
        Get-ChildItem $EmbedDir -Force | Where-Object { $_.Name -ne ".gitkeep" } | Remove-Item -Recurse -Force
        Write-Info "Cleared embedded web assets in $EmbedDir"
    }
    Write-Ok "Clean complete"
}

# ---------- cross ----------
function Invoke-Cross {
    param([string]$Tags = "")

    Write-Info "Cross-compiling for Windows targets (CGO_ENABLED=0, purego runtime loading)..."
    Write-Host ""

    foreach ($arch in @("amd64", "arm64")) {
        Write-Info "--- windows/$arch ---"
        $env:kocort_OUTPUT = ""
        Invoke-Build -Tags $Tags -TargetOS "windows" -TargetArch $arch
        Write-Host ""
    }

    Write-Host ""
    Write-Ok "Cross-compilation complete. Output in $DistDir\"
    Get-ChildItem $DistDir -Recurse -File | Format-Table FullName, Length -AutoSize
}

# ---------- main ----------
function Main {
    $targets = $Target -split ","

    $wantCross = $false
    $actions   = @()

    foreach ($t in $targets) {
        switch ($t.Trim().ToLower()) {
            "all"      { } # uses kocort_BUILD_TAGS
            "test"     { $actions += "test" }
            "vet"      { $actions += "vet" }
            "clean"    { $actions += "clean" }
            "cross"    { $wantCross = $true }
            "build"    { } # default, no-op
            default    { Write-Fail "Unknown target: $t. Valid: build, all, test, vet, clean, cross" }
        }
    }

    # Assemble tags
    $tags = $env:kocort_BUILD_TAGS
    if (-not $tags) { $tags = "" }

    Write-Info "kocort build - version $Version ($Commit) [$BuildDate]"
    Write-Info "Platform: windows/$(Get-GoArch) | Go: $(go version | ForEach-Object { ($_ -split ' ')[2] })"
    Write-Info "CGO: disabled (llama.cpp loaded via purego at runtime)"
    Write-Host ""

    if ($actions -contains "clean") {
        Invoke-Clean
        return
    }

    foreach ($action in $actions) {
        switch ($action) {
            "test" { Invoke-Test -Tags $tags }
            "vet"  { Invoke-Vet  -Tags $tags }
        }
    }

    # If no explicit test/vet action, default to build
    if ($actions.Count -eq 0) {
        if ($wantCross) {
            Invoke-Cross -Tags $tags
        } else {
            Invoke-Build -Tags $tags
        }
    }
}

Main
