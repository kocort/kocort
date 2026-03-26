#!/bin/bash
# Bundle playwright driver (+ optionally browsers) alongside kocort binary.
#
# This script downloads the Playwright driver into a local directory so that
# the resulting distribution is self-contained — no internet access needed at
# runtime, and no system-wide Node.js installation required.
#
# Usage:
#   ./scripts/bundle-browser.sh                     # Driver only (use system Chrome/Edge)
#   ./scripts/bundle-browser.sh --with-chromium      # Driver + bundled Chromium
#   ./scripts/bundle-browser.sh --help               # Show help
#
# The output goes into dist/<platform>/playwright-driver/.
# At runtime, set DriverDir or PLAYWRIGHT_DRIVER_PATH to this directory.
#
# Environment variables:
#   KOCORT_BROWSER_DIST   - Override output directory (default: dist/<platform>)
#   PLAYWRIGHT_VERSION    - Override playwright-go version (read from go.mod)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# ---------- colours ----------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}==>${NC} $*"; }
ok()    { echo -e "${GREEN}==>${NC} $*"; }
warn()  { echo -e "${YELLOW}WARNING:${NC} $*"; }
fail()  { echo -e "${RED}ERROR:${NC} $*" >&2; exit 1; }

# ---------- platform ----------
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH_LABEL=amd64 ;;
    aarch64) ARCH_LABEL=arm64 ;;
    arm64)   ARCH_LABEL=arm64 ;;
    *)       ARCH_LABEL="$ARCH" ;;
esac

PLATFORM="${OS}_${ARCH_LABEL}"

# ---------- defaults ----------
DIST_DIR="${KOCORT_BROWSER_DIST:-$PROJECT_ROOT/dist/$PLATFORM}"
DRIVER_DIR="$DIST_DIR/playwright-driver"
WITH_CHROMIUM=0

# ---------- detect playwright-go version from go.mod ----------
detect_playwright_version() {
    local version
    version=$(grep 'playwright-community/playwright-go' "$PROJECT_ROOT/go.mod" \
        | head -1 | awk '{print $2}')
    if [ -z "$version" ]; then
        fail "Could not detect playwright-go version from go.mod"
    fi
    echo "$version"
}

PW_VERSION="${PLAYWRIGHT_VERSION:-$(detect_playwright_version)}"

# ---------- parse args ----------
while [ $# -gt 0 ]; do
    case "$1" in
        --with-chromium)
            WITH_CHROMIUM=1
            shift ;;
        --help|-h)
            cat <<'EOF'
Bundle Playwright driver for kocort distribution.

Usage:
  ./scripts/bundle-browser.sh [OPTIONS]

Options:
  (no flags)          Download driver only; use system Chrome/Edge at runtime
  --with-chromium     Also download bundled Chromium browser (~150-200MB)
  --help              Show this help

Runtime modes:
  1. System browser (recommended, smallest bundle):
     - Bundle driver only (this script without flags)
     - Set config: browserUseSystem=true, browserSkipInstall=true
     - Chrome or Edge must be installed on the target system

  2. Bundled Chromium (fully offline):
     - Bundle driver + chromium (--with-chromium flag)
     - No system browser required
     - Distribution will be ~200MB larger

Environment:
  KOCORT_BROWSER_DIST   Override output directory
  PLAYWRIGHT_VERSION    Override playwright-go module version

Examples:
  # Driver only — smallest bundle, relies on system Chrome/Edge
  ./scripts/bundle-browser.sh

  # Driver + Chromium — fully self-contained
  ./scripts/bundle-browser.sh --with-chromium

  # Custom output dir
  KOCORT_BROWSER_DIST=./my-dist ./scripts/bundle-browser.sh
EOF
            exit 0 ;;
        *)
            fail "Unknown option: $1. Use --help for usage." ;;
    esac
done

# ---------- main ----------
info "Bundling Playwright driver for kocort"
info "  Platform:             $PLATFORM"
info "  playwright-go:        $PW_VERSION"
info "  Driver output:        $DRIVER_DIR"
info "  Include Chromium:     $([ $WITH_CHROMIUM -eq 1 ] && echo 'yes' || echo 'no (system browser)')"
echo ""

mkdir -p "$DRIVER_DIR"

# Build a small Go helper to download via the playwright-go library itself,
# ensuring exact version match with the project's dependency.
HELPER_DIR=$(mktemp -d)
trap 'rm -rf "$HELPER_DIR"' EXIT

cat > "$HELPER_DIR/main.go" <<GOEOF
package main

import (
	"fmt"
	"os"

	playwright "github.com/playwright-community/playwright-go"
)

func main() {
	driverDir := os.Getenv("PW_DRIVER_DIR")
	skipBrowsers := os.Getenv("PW_SKIP_BROWSERS") == "1"

	opts := &playwright.RunOptions{
		DriverDirectory:     driverDir,
		SkipInstallBrowsers: skipBrowsers,
		Browsers:            []string{"chromium"},
		Verbose:             true,
	}

	if err := playwright.Install(opts); err != nil {
		fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Install completed successfully.")
}
GOEOF

cat > "$HELPER_DIR/go.mod" <<MODEOF
module bundle-helper

go 1.22

require github.com/playwright-community/playwright-go $PW_VERSION
MODEOF

info "Resolving dependencies for install helper..."
(cd "$HELPER_DIR" && go mod tidy 2>&1)

SKIP_BROWSERS="1"
if [ $WITH_CHROMIUM -eq 1 ]; then
    SKIP_BROWSERS="0"
fi

info "Downloading Playwright driver..."
(cd "$HELPER_DIR" && PW_DRIVER_DIR="$DRIVER_DIR" PW_SKIP_BROWSERS="$SKIP_BROWSERS" go run main.go)

# ---------- verify ----------
if [ ! -d "$DRIVER_DIR" ]; then
    fail "Driver directory was not created: $DRIVER_DIR"
fi

NODE_BIN="$DRIVER_DIR/node/node"
if [ "$OS" = "darwin" ] || [ "$OS" = "linux" ]; then
    if [ ! -x "$NODE_BIN" ]; then
        # Try alternative layout
        NODE_BIN=$(find "$DRIVER_DIR" -name "node" -type f 2>/dev/null | head -1)
    fi
fi

if [ -n "$NODE_BIN" ] && [ -x "$NODE_BIN" ]; then
    ok "Node binary found: $NODE_BIN"
else
    warn "Could not verify node binary in driver directory"
fi

# ---------- summary ----------
echo ""
DRIVER_SIZE=$(du -sh "$DRIVER_DIR" | awk '{print $1}')
ok "Playwright driver bundled successfully!"
ok "  Location: $DRIVER_DIR"
ok "  Size:     $DRIVER_SIZE"
echo ""

if [ $WITH_CHROMIUM -eq 1 ]; then
    info "Distribution includes bundled Chromium."
    info "Runtime config:"
    info "  browserDriverDir: \"$DRIVER_DIR\"    (or PLAYWRIGHT_DRIVER_PATH env)"
    info "  browserAutoInstall: false"
    info "  browserSkipInstall: true"
else
    info "Driver-only mode. System Chrome or Edge is required at runtime."
    info "Runtime config (kocort.json):"
    echo ""
    cat <<JSONEOF
  "tools": {
    "browserDriverDir": "./playwright-driver",
    "browserUseSystem": true,
    "browserSkipInstall": true
  }
JSONEOF
    echo ""
    info "Browser fallback order: Chrome → Edge → bundled Chromium (if available)"
fi
