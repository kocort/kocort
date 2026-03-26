#!/bin/bash
# Cross-platform build script for kocort (macOS / Linux)
#
# Usage:
#   ./scripts/build.sh                  # Build default llama.cpp binary for current platform
#   ./scripts/build.sh --llamacpp       # Alias of the default build
#   ./scripts/build.sh --all            # Alias of the default build plus extra tags from kocort_BUILD_TAGS
#   ./scripts/build.sh --test           # Run tests (with llamacpp tag)
#   ./scripts/build.sh --vet            # Run go vet
#   ./scripts/build.sh --clean          # Clean build cache
#   ./scripts/build.sh --cross          # Cross-compile for all platforms
#
# Environment variables:
#   kocort_VERSION        - Version string (default: git describe or "dev")
#   kocort_BUILD_TAGS     - Extra build tags (space-separated)
#   kocort_OUTPUT         - Output binary path (default: ./dist/<name>)
#   kocort_CGO_OPTFLAGS   - C/C++ optimization flags (default: -O3)
#   kocort_PARALLEL       - Build parallelism (default: num CPUs)
#   kocort_SKIP_WEB       - Set to 1 to skip web build/embed refresh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
DIST_DIR="$PROJECT_ROOT/dist"
WEB_DIR="$PROJECT_ROOT/web"
EMBED_DIR="$PROJECT_ROOT/api/static/dist"

# ---------- version ----------
VERSION="${kocort_VERSION:-}"
if [ -z "$VERSION" ]; then
    VERSION=$(cd "$PROJECT_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo "dev")
fi

COMMIT=$(cd "$PROJECT_ROOT" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

# ---------- defaults ----------
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
DEFAULT_GOARCH=""
case "$ARCH" in
    x86_64)  DEFAULT_GOARCH=amd64 ;;
    aarch64) DEFAULT_GOARCH=arm64 ;;
    arm64)   DEFAULT_GOARCH=arm64 ;;
    *)       DEFAULT_GOARCH="$ARCH" ;;
esac
DEFAULT_GOOS=""
case "$OS" in
    darwin) DEFAULT_GOOS=darwin ;;
    linux)  DEFAULT_GOOS=linux ;;
    mingw*|msys*|cygwin*) DEFAULT_GOOS=windows ;;
    *) DEFAULT_GOOS="$OS" ;;
esac
GOARCH="${GOARCH:-$DEFAULT_GOARCH}"
GOOS="${GOOS:-$DEFAULT_GOOS}"

OPT_FLAGS="${kocort_CGO_OPTFLAGS:--O3}"
PARALLEL="${kocort_PARALLEL:-$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)}"

# ---------- colours ----------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}==>${NC} $*"; }
ok()    { echo -e "${GREEN}==>${NC} $*"; }
warn()  { echo -e "${YELLOW}WARNING:${NC} $*"; }
fail()  { echo -e "${RED}ERROR:${NC} $*" >&2; exit 1; }

# ---------- helpers ----------
require_cgo_compiler() {
    if command -v gcc &>/dev/null; then
        return 0
    elif command -v clang &>/dev/null; then
        return 0
    elif command -v cc &>/dev/null; then
        return 0
    fi
    fail "No C/C++ compiler found. Install gcc, clang, or Xcode CLI tools."
}

setup_cgo_env() {
    export CGO_ENABLED=1
    export CGO_CFLAGS="${CGO_CFLAGS:-$OPT_FLAGS}"
    export CGO_CXXFLAGS="${CGO_CXXFLAGS:-$OPT_FLAGS}"

    if [ "$GOOS" = "darwin" ]; then
        local MIN_VER="14.0"
        export CGO_CFLAGS="$CGO_CFLAGS -mmacosx-version-min=$MIN_VER"
        export CGO_CXXFLAGS="$CGO_CXXFLAGS -mmacosx-version-min=$MIN_VER"
        export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=$MIN_VER"
    fi
}

build_ldflags() {
    echo "-s -w -X=kocort/version.Version=$VERSION -X=kocort/version.Commit=$COMMIT -X=kocort/version.BuildDate=$BUILD_DATE"
}

build_web() {
    if [ "${kocort_SKIP_WEB:-0}" = "1" ]; then
        warn "Skipping web build because kocort_SKIP_WEB=1"
        return
    fi
    if ! command -v npm >/dev/null 2>&1; then
        fail "npm is required to build the embedded web UI"
    fi
    if [ ! -d "$WEB_DIR/node_modules" ]; then
        fail "web/node_modules is missing. Run 'npm install' in web/ first."
    fi

    info "Building web UI for embed"
    (
        cd "$WEB_DIR"
        npm run build
    )

    if [ ! -d "$WEB_DIR/out" ]; then
        fail "web build finished but web/out was not generated"
    fi

    mkdir -p "$EMBED_DIR"
    find "$EMBED_DIR" -mindepth 1 -not -name '.gitkeep' -exec rm -rf {} +
    cp -R "$WEB_DIR/out"/. "$EMBED_DIR"/
    ok "Embedded web assets refreshed: $EMBED_DIR"
}

output_name() {
    local suffix=""
    if [ "$GOOS" = "windows" ]; then suffix=".exe"; fi
    echo "kocort${suffix}"
}

# ---------- build actions ----------
do_build() {
    local tags="${1:-}"
    local target_goos="${2:-$GOOS}"
    local target_goarch="${3:-$GOARCH}"

    if [ "$target_goos" = "$GOOS" ] && [ "$target_goarch" = "$GOARCH" ]; then
        build_web
    else
        warn "Skipping web rebuild for cross target $target_goos/$target_goarch; using existing embedded assets"
    fi

    local cgo_needed=0
    if echo "$tags" | grep -q "llamacpp"; then
        cgo_needed=1
    fi

    if [ "$cgo_needed" -eq 1 ]; then
        require_cgo_compiler
        setup_cgo_env
    else
        export CGO_ENABLED=0
    fi

    local out_name
    out_name=$(output_name "$tags")
    local out_path="${kocort_OUTPUT:-$DIST_DIR/${target_goos}_${target_goarch}/$out_name}"
    mkdir -p "$(dirname "$out_path")"

    local ldflags
    ldflags=$(build_ldflags)

    local build_args=(-trimpath -ldflags "$ldflags" -o "$out_path")
    if [ -n "$tags" ]; then
        build_args+=(-tags "$tags")
    fi
    if [ "$GOOS" = "linux" ]; then
        build_args+=(-buildmode=pie)
    fi
    build_args+=(-p "$PARALLEL")

    info "Building kocort"
    info "  GOOS=$target_goos GOARCH=$target_goarch"
    info "  CGO_ENABLED=$CGO_ENABLED"
    info "  Tags: ${tags:-<none>}"
    info "  Output: $out_path"
    echo ""

    cd "$PROJECT_ROOT"
    GOOS="$target_goos" GOARCH="$target_goarch" \
        go build "${build_args[@]}" ./cmd/kocort

    ok "Build successful: $out_path"

    if [ "$cgo_needed" -eq 1 ]; then
        echo ""
        info "To use GPU backends at runtime, set:"
        info "  export GGML_LIBRARY_PATH=/path/to/gpu/libs"
    fi
}

do_test() {
    local tags="${1:-llamacpp}"
    setup_cgo_env
    info "Running tests (tags: $tags)"
    cd "$PROJECT_ROOT"

    info "=== llama package tests ==="
    go test -tags "$tags" -v -count=1 ./internal/llama/...

    info "=== cerebellum package tests ==="
    go test -tags "$tags" -v -count=1 ./internal/cerebellum/...

    info "=== all package tests ==="
    go test -tags "$tags" -count=1 -timeout 120s ./...

    ok "All tests passed"
}

do_vet() {
    local tags="${1:-llamacpp}"
    setup_cgo_env
    info "Running go vet (tags: $tags)"
    cd "$PROJECT_ROOT"
    go vet -tags "$tags" ./...
    ok "go vet passed"
}

do_clean() {
    info "Cleaning build cache..."
    go clean -cache
    if [ -d "$DIST_DIR" ]; then
        rm -rf "$DIST_DIR"
        info "Removed $DIST_DIR"
    fi
    if [ -d "$EMBED_DIR" ]; then
        find "$EMBED_DIR" -mindepth 1 -not -name '.gitkeep' -exec rm -rf {} +
        info "Cleared embedded web assets in $EMBED_DIR"
    fi
    ok "Clean complete"
}

do_cross() {
    local tags="${1:-}"
    info "Cross-compiling for all platforms..."
    echo ""

    if echo "$tags" | grep -q "llamacpp"; then
        warn "llamacpp (CGo) builds are only compiled for the native platform: $GOOS/$GOARCH"
        warn "Use the target platform or GitHub Actions release workflows for six-platform artifacts."
        echo ""
        info "--- $GOOS/$GOARCH (llamacpp) ---"
        do_build "$tags" "$GOOS" "$GOARCH"
    else
        local platforms=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64" "windows/amd64" "windows/arm64")
        for platform in "${platforms[@]}"; do
            local p_os="${platform%%/*}"
            local p_arch="${platform##*/}"
            info "--- $p_os/$p_arch ---"
            CGO_ENABLED=0 kocort_OUTPUT="" do_build "$tags" "$p_os" "$p_arch"
            echo ""
        done
    fi

    echo ""
    ok "Cross-compilation complete. Output in $DIST_DIR/"
    ls -la "$DIST_DIR"/*/
}

# ---------- usage ----------
usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Build script for kocort — cross-platform Go build with default llama.cpp CGo support.

Options:
    (no flags)        Build default binary (llama.cpp enabled, CGO_ENABLED=1)
    --llamacpp        Alias of the default build
    --all             Alias of the default build plus extra tags from kocort_BUILD_TAGS
  --test            Run full test suite
  --vet             Run go vet
  --clean           Clean build artifacts and cache
  --cross           Cross-compile for all platforms
  --help            Show this help

Environment:
  kocort_VERSION        Override version string
  kocort_BUILD_TAGS     Extra build tags
  kocort_OUTPUT         Override output path
  kocort_CGO_OPTFLAGS   C/C++ optimization flags (default: -O3)
  kocort_PARALLEL       Build parallelism

Examples:
    # Default build with llama.cpp local inference
  ./scripts/build.sh

    # Explicit llama.cpp build (same as default)
  ./scripts/build.sh --llamacpp

    # Cross-compile non-CGo variants for all platforms
  ./scripts/build.sh --cross

    # Build native llama.cpp artifact from cross mode
  ./scripts/build.sh --cross --llamacpp

  # Build with custom version
    kocort_VERSION=1.2.3 ./scripts/build.sh

  # Run tests
  ./scripts/build.sh --test
EOF
}

# ---------- main ----------
main() {
    local action=""
    local tags="${kocort_BUILD_TAGS:-}"
    local want_llamacpp=1
    local want_cross=0

    while [ $# -gt 0 ]; do
        case "$1" in
            --llamacpp)
                want_llamacpp=1
                shift ;;
            --all)
                want_llamacpp=1
                shift ;;
            --test)
                action="test"
                shift ;;
            --vet)
                action="vet"
                shift ;;
            --clean)
                action="clean"
                shift ;;
            --cross)
                want_cross=1
                shift ;;
            --help|-h)
                usage
                exit 0 ;;
            *)
                fail "Unknown option: $1. Use --help for usage." ;;
        esac
    done

    if [ "$want_llamacpp" -eq 1 ]; then
        if [ -n "$tags" ]; then
            tags="$tags llamacpp"
        else
            tags="llamacpp"
        fi
    fi

    info "kocort build — version $VERSION ($COMMIT) [$BUILD_DATE]"
    info "Platform: $GOOS/$GOARCH | Go: $(go version | awk '{print $3}')"
    echo ""

    case "${action:-build}" in
        test)  do_test "$tags" ;;
        vet)   do_vet "$tags" ;;
        clean) do_clean ;;
        build)
            if [ "$want_cross" -eq 1 ]; then
                do_cross "$tags"
            else
                do_build "$tags"
            fi
            ;;
    esac
}

main "$@"
