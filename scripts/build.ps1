<#
.SYNOPSIS
    Cross-platform build script for kocort (Windows PowerShell)

.DESCRIPTION
    Builds the kocort binary with default llama.cpp CGo support.
    Mirrors the functionality of scripts/build.sh for Windows.

.PARAMETER Target
    Build target(s). Comma-separated. Valid values:
    build       - Build the default llama.cpp kocort binary
    llamacpp    - Alias of the default build
    all         - Alias of the default build plus extra tags from kocort_BUILD_TAGS
      test        - Run tests (with llamacpp tag)
      vet         - Run go vet
      clean       - Clean build artifacts
      cross       - Cross-compile for win-amd64 and win-arm64

.EXAMPLE
    # Default build with llama.cpp support
    .\scripts\build.ps1

    # Explicit llama.cpp build (same as default)
    .\scripts\build.ps1 llamacpp

    # Build with llamacpp + run tests
    .\scripts\build.ps1 llamacpp,test

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

function Test-CgoCompiler {
    $found = $false
    foreach ($cc in @("gcc", "clang", "cc")) {
        if (Get-Command $cc -ErrorAction SilentlyContinue) {
            $found = $true
            break
        }
    }
    if (-not $found) {
        Write-Fail "No C/C++ compiler found. Install MinGW-w64 (choco install mingw) or TDM-GCC."
    }
}

function Set-CgoEnv {
    $env:CGO_ENABLED = "1"
    $optFlags = if ($env:kocort_CGO_OPTFLAGS) { $env:kocort_CGO_OPTFLAGS } else { "-O3" }
    if (-not $env:CGO_CFLAGS)   { $env:CGO_CFLAGS   = $optFlags }
    if (-not $env:CGO_CXXFLAGS) { $env:CGO_CXXFLAGS = $optFlags }
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

    $cgoNeeded = $Tags -match "llamacpp"

    if ($cgoNeeded) {
        Test-CgoCompiler
        Set-CgoEnv
    } else {
        $env:CGO_ENABLED = "0"
    }

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
    Write-Info "  CGO_ENABLED=$env:CGO_ENABLED"
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

    if ($cgoNeeded) {
        Write-Host ""
        Write-Info "To use GPU backends at runtime, set:"
        Write-Info '  $env:GGML_LIBRARY_PATH = "C:\path\to\gpu\libs"'
    }
}

# ---------- test ----------
function Invoke-Test {
    param([string]$Tags = "llamacpp")

    Set-CgoEnv
    Write-Info "Running tests (tags: $Tags)"
    Push-Location $ProjectRoot
    try {
        Write-Info "=== llama package tests ==="
        & go test -tags $Tags -v -count=1 ./internal/llama/...
        if ($LASTEXITCODE -ne 0) { Write-Fail "llama tests failed" }

        Write-Info "=== cerebellum package tests ==="
        & go test -tags $Tags -v -count=1 ./internal/cerebellum/...
        if ($LASTEXITCODE -ne 0) { Write-Fail "cerebellum tests failed" }

        Write-Info "=== all package tests ==="
        & go test -tags $Tags -count=1 -timeout 120s ./...
        if ($LASTEXITCODE -ne 0) { Write-Fail "tests failed" }
    } finally {
        Pop-Location
    }
    Write-Ok "All tests passed"
}

# ---------- vet ----------
function Invoke-Vet {
    param([string]$Tags = "llamacpp")

    Set-CgoEnv
    Write-Info "Running go vet (tags: $Tags)"
    Push-Location $ProjectRoot
    try {
        & go vet -tags $Tags ./...
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

    Write-Info "Cross-compiling for Windows targets..."
    Write-Host ""

    if ($Tags -match '(^|\s)llamacpp(\s|$)') {
        $nativeArch = Get-GoArch
        Write-Warn "llamacpp (CGo) builds only compiled for native arch: windows/$nativeArch"
        Write-Warn "Use the target platform or GitHub Actions release workflows for full Windows artifacts."
        Write-Host ""

        Write-Info "--- windows/$nativeArch (llamacpp) ---"
        $env:kocort_OUTPUT = ""
        Invoke-Build -Tags $Tags -TargetOS "windows" -TargetArch $nativeArch
    } else {
        foreach ($arch in @("amd64", "arm64")) {
            Write-Info "--- windows/$arch ---"
            $env:kocort_OUTPUT = ""
            Invoke-Build -Tags $Tags -TargetOS "windows" -TargetArch $arch
            Write-Host ""
        }
    }

    Write-Host ""
    Write-Ok "Cross-compilation complete. Output in $DistDir\"
    Get-ChildItem $DistDir -Recurse -File | Format-Table FullName, Length -AutoSize
}

# ---------- main ----------
function Main {
    $targets = $Target -split ","

    $wantLlamacpp = $true
    $wantCross    = $false
    $actions      = @()

    foreach ($t in $targets) {
        switch ($t.Trim().ToLower()) {
            "llamacpp" { $wantLlamacpp = $true }
            "all"      { $wantLlamacpp = $true }
            "test"     { $actions += "test" }
            "vet"      { $actions += "vet" }
            "clean"    { $actions += "clean" }
            "cross"    { $wantCross = $true }
            "build"    { } # default, no-op
            default    { Write-Fail "Unknown target: $t. Valid: build, llamacpp, all, test, vet, clean, cross" }
        }
    }

    # Assemble tags
    $tags = $env:kocort_BUILD_TAGS
    if (-not $tags) { $tags = "" }
    if ($wantLlamacpp) {
        $tags = ("$tags llamacpp").Trim()
    }

    Write-Info "kocort build - version $Version ($Commit) [$BuildDate]"
    Write-Info "Platform: windows/$(Get-GoArch) | Go: $(go version | ForEach-Object { ($_ -split ' ')[2] })"
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
