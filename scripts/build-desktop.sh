#!/bin/bash
# Build script for kocort desktop (tray/menubar) editions.
#
# Usage:
#   ./scripts/build-desktop.sh                    # Build for current platform
#   ./scripts/build-desktop.sh --windows          # Cross-compile Windows tray .exe
#   ./scripts/build-desktop.sh --macos            # Build macOS .app bundle for native arch
#   ./scripts/build-desktop.sh --macos-universal  # Build macOS .app bundle (universal)
#   ./scripts/build-desktop.sh --macos-dmg        # Build macOS .dmg installer
#   ./scripts/build-desktop.sh --all              # Build all platforms
#
# Prerequisites:
#   - Go 1.23+
#   - For Windows: go get github.com/energye/systray
#   - For macOS .app: Xcode CLI tools (swift, codesign)
#   - For macOS .dmg: create-dmg (brew install create-dmg)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
DIST_DIR="$PROJECT_ROOT/dist"
DESKTOP_DIR="$PROJECT_ROOT/desktop"
ICONS_DIR="$DESKTOP_DIR/icons"

# ---------- external tool binaries (rg, fd) ----------
# Pre-built binaries can be placed in bin/tools/ under the project root,
# or resolved from the local system PATH during the build.
#
# Expected layout for macOS universal:
#   bin/tools/darwin_universal/rg
#   bin/tools/darwin_universal/fd
# Per-arch:
#   bin/tools/darwin_arm64/rg
#   bin/tools/darwin_amd64/rg
# Windows:
#   bin/tools/windows_amd64/rg.exe
#   bin/tools/windows_amd64/fd.exe
TOOL_BINS_DIR="$PROJECT_ROOT/bin/tools"
TOOL_BINS_NAMES=(rg fd)

# ---------- playwright browser driver ----------
# Controls whether the Playwright driver (+ optionally Chromium) is bundled.
# Set via --with-browser or --with-chromium flags, or KOCORT_WITH_BROWSER env.
# Values: "" (skip), "driver" (driver only ~50MB), "chromium" (driver+chromium ~200MB)
WITH_BROWSER="${KOCORT_WITH_BROWSER:-}"

# ---------- signing ----------
# Set --no-sign or KOCORT_NO_SIGN=1 to produce an unsigned .app bundle.
# You can then sign it later with your own identity:
#   codesign --deep --force --options runtime --sign "Developer ID Application: ..." dist/Kocort.app
SKIP_SIGN="${KOCORT_NO_SIGN:-0}"

# ---------- version ----------
VERSION="${KOCORT_VERSION:-}"
if [ -z "$VERSION" ]; then
    VERSION=$(cd "$PROJECT_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo "dev")
fi
COMMIT=$(cd "$PROJECT_ROOT" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

# ---------- colours ----------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}==>${NC} $*" >&2; }
ok()    { echo -e "${GREEN}==>${NC} $*" >&2; }
warn()  { echo -e "${YELLOW}WARNING:${NC} $*" >&2; }
fail()  { echo -e "${RED}ERROR:${NC} $*" >&2; exit 1; }

require_cgo_compiler() {
    if command -v clang &>/dev/null; then
        return 0
    elif command -v cc &>/dev/null; then
        return 0
    elif command -v gcc &>/dev/null; then
        return 0
    fi
    fail "No C/C++ compiler found. Install Xcode CLI tools: xcode-select --install"
}

setup_macos_cgo_env() {
    local target_arch="$1"
    local min_ver="14.0"
    local cc_bin="${CC:-$(command -v clang || command -v cc || true)}"
    local cxx_bin="${CXX:-$(command -v clang++ || command -v c++ || command -v clang || command -v cc || true)}"
    if [ -z "$cc_bin" ]; then
        fail "Unable to find clang/cc for macOS CGO build"
    fi
    if [ -z "$cxx_bin" ]; then
        fail "Unable to find clang++/c++ for macOS CGO build"
    fi

    export CGO_ENABLED=1
    export CC="$cc_bin"
    export CXX="$cxx_bin"

    # Reset flags each time to avoid accumulating -arch from previous calls
    local base_cflags="-O3 -mmacosx-version-min=$min_ver"
    local base_cxxflags="-O3 -std=c++17 -mmacosx-version-min=$min_ver"
    local base_ldflags="-mmacosx-version-min=$min_ver -lc++"

    case "$target_arch" in
        amd64)
            base_cflags="$base_cflags -arch x86_64"
            base_cxxflags="$base_cxxflags -arch x86_64"
            base_ldflags="$base_ldflags -arch x86_64"
            ;;
        arm64)
            base_cflags="$base_cflags -arch arm64"
            base_cxxflags="$base_cxxflags -arch arm64"
            base_ldflags="$base_ldflags -arch arm64"
            ;;
    esac

    export CGO_CFLAGS="$base_cflags"
    export CGO_CXXFLAGS="$base_cxxflags"
    export CGO_LDFLAGS="$base_ldflags"
}

normalize_macos_arch() {
    local arch="$1"
    case "$arch" in
        x86_64) echo "amd64" ;;
        aarch64) echo "arm64" ;;
        *) echo "$arch" ;;
    esac
}

# ---------- common ----------
build_ldflags() {
    echo "-s -w -X=kocort/version.Version=$VERSION -X=kocort/version.Commit=$COMMIT -X=kocort/version.BuildDate=$BUILD_DATE"
}

ensure_tray_icon_windows() {
    local ico_path="$PROJECT_ROOT/cmd/kocort-desktop/tray.ico"
    if [ -f "$ico_path" ]; then
        return 0
    fi
    # Try to generate .ico from PNG using ImageMagick
    if command -v convert &>/dev/null && [ -f "$ICONS_DIR/tray.png" ]; then
        info "Generating tray.ico from tray.png..."
        convert "$ICONS_DIR/tray.png" -define icon:auto-resize=64,48,32,16 "$ico_path"
        return 0
    fi
    if command -v magick &>/dev/null && [ -f "$ICONS_DIR/tray.png" ]; then
        info "Generating tray.ico from tray.png..."
        magick "$ICONS_DIR/tray.png" -define icon:auto-resize=64,48,32,16 "$ico_path"
        return 0
    fi
    # Use PNG as fallback — the systray library supports PNG too
    if [ -f "$ICONS_DIR/tray.png" ]; then
        warn "No ImageMagick found. Copying tray.png as tray.ico (systray supports PNG bytes)."
        cp "$ICONS_DIR/tray.png" "$ico_path"
        return 0
    fi
    fail "No tray icon found. Place tray.png in desktop/icons/ or tray.ico in cmd/kocort-desktop/"
}

# ---------- Windows build ----------
build_windows() {
    local arch="${1:-amd64}"
    info "Building Windows desktop (tray) — windows/$arch"

    local out_dir="$DIST_DIR/windows_${arch}"
    local out_path="$out_dir/kocort-desktop.exe"
    mkdir -p "$out_dir"

    local ldflags
    ldflags="$(build_ldflags) -H windowsgui"

    cd "$PROJECT_ROOT"
    CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
        go build \
        -trimpath \
        -ldflags "$ldflags" \
        -o "$out_path" \
        ./cmd/kocort-desktop

    # Embed external tool binaries (rg, fd)
    embed_tool_bins_windows "$out_dir"

    ok "Windows build: $out_path"
    ls -lh "$out_path" >&2
}

# ---------- Linux build ----------
build_linux() {
    local arch="${1:-$(uname -m)}"
    if [ "$(uname -s)" != "Linux" ]; then
        fail "Linux desktop build currently requires a native Linux/Ubuntu host because the project bundles native llama.cpp (CGo) components"
    fi
    case "$arch" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
    esac

    info "Building Linux desktop (Ubuntu tray) — linux/$arch"

    local out_dir="$DIST_DIR/linux_${arch}"
    local out_path="$out_dir/kocort-desktop"
    mkdir -p "$out_dir"

    local ldflags
    ldflags="$(build_ldflags)"

    cd "$PROJECT_ROOT"
    GOOS=linux GOARCH="$arch" \
        go build \
        -trimpath \
        -ldflags "$ldflags" \
        -o "$out_path" \
        ./cmd/kocort-desktop

    chmod +x "$out_path"

    cp "$DESKTOP_DIR/linux/kocort.desktop" "$out_dir/kocort.desktop"
    if [ -f "$ICONS_DIR/icon.png" ]; then
        cp "$ICONS_DIR/icon.png" "$out_dir/kocort.png"
    fi

    ok "Linux build: $out_path"
    ls -lh "$out_path" >&2
}

# ---------- macOS build ----------
build_macos_go_binary() {
    local arch="${1:-$(uname -m)}"
    case "$arch" in
        x86_64) arch="amd64" ;;
        arm64)  arch="arm64" ;;
    esac
    local go_arch="$arch"
    case "$go_arch" in
        amd64) go_arch="amd64" ;;
        arm64) go_arch="arm64" ;;
    esac

    info "Building kocort Go binary for macOS/$arch..."

    local out_path="$DIST_DIR/macos_${arch}/kocort"
    mkdir -p "$(dirname "$out_path")"

    local ldflags
    ldflags=$(build_ldflags)

    require_cgo_compiler
    setup_macos_cgo_env "$go_arch"

    cd "$PROJECT_ROOT"
    GOOS=darwin GOARCH="$go_arch" \
        go build \
        -trimpath \
        -tags llamacpp \
        -ldflags "$ldflags" \
        -o "$out_path" \
        ./cmd/kocort-desktop

    ok "macOS Go binary: $out_path"
}

build_macos_universal_binary() {
    info "Building macOS universal (fat) binary..."

    local amd64_bin="$DIST_DIR/macos_amd64/kocort"
    local arm64_bin="$DIST_DIR/macos_arm64/kocort"
    local universal_bin="$DIST_DIR/macos_universal/kocort"
    mkdir -p "$(dirname "$universal_bin")"

    # Build both architectures
    build_macos_go_binary "amd64"
    build_macos_go_binary "arm64"

    # Merge with lipo
    if command -v lipo &>/dev/null; then
        lipo -create -output "$universal_bin" "$amd64_bin" "$arm64_bin"
        ok "Universal binary: $universal_bin"
    else
        warn "lipo not found, using native arch binary only"
        cp "$arm64_bin" "$universal_bin" 2>/dev/null || cp "$amd64_bin" "$universal_bin"
    fi
}

build_macos_swift_app() {
    info "Building macOS Swift shell app..."

    local swift_dir="$DESKTOP_DIR/macos/KocortApp"

    if ! command -v swift &>/dev/null; then
        fail "Swift compiler not found. Install Xcode CLI tools: xcode-select --install"
    fi

    cd "$swift_dir"
    swift build -c release 2>&1 | tail -5

    ok "Swift app compiled"
}

build_macos_app_bundle() {
    local arch="${1:-universal}"
    arch="$(normalize_macos_arch "$arch")"
    info "Creating macOS .app bundle..."

    local app_dir="$DIST_DIR/Kocort.app"
    local contents="$app_dir/Contents"
    local macos_dir="$contents/MacOS"
    local resources="$contents/Resources"

    # Clean previous build
    rm -rf "$app_dir"
    mkdir -p "$macos_dir" "$resources"

    # 1. Copy Info.plist
    cp "$DESKTOP_DIR/macos/KocortApp/Info.plist" "$contents/Info.plist"

    # Patch Info.plist with actual version and bundle ID
    local bundle_id="${KOCORT_BUNDLE_ID:-com.kocort.app}"
    sed -i '' "s/\$(PRODUCT_BUNDLE_IDENTIFIER)/$bundle_id/g" "$contents/Info.plist" 2>/dev/null || true
    # Update version
    /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$contents/Info.plist" 2>/dev/null || true

    # 2. Build Swift shell → main executable
    local swift_dir="$DESKTOP_DIR/macos/KocortApp"
    cd "$swift_dir"
    info "Building Swift shell app..."
    swift build -c release 2>&1 | tail -5

    # Find the built Swift binary
    local swift_bin=""
    # Strategy 1: use --show-bin-path (fast, no rebuild)
    local bin_path
    bin_path=$(swift build -c release --show-bin-path 2>/dev/null || true)
    if [ -n "$bin_path" ] && [ -f "$bin_path/KocortApp" ]; then
        swift_bin="$bin_path/KocortApp"
    fi
    # Strategy 2: search in .build/release
    if [ -z "$swift_bin" ]; then
        swift_bin=$(find "$swift_dir/.build" -path '*/release/KocortApp' -type f ! -path '*.dSYM*' 2>/dev/null | head -1)
    fi
    # Strategy 3: search in .build (any config)
    if [ -z "$swift_bin" ]; then
        swift_bin=$(find "$swift_dir/.build" -name "KocortApp" -type f ! -path '*.dSYM*' ! -path '*/ModuleCache/*' 2>/dev/null | head -1)
    fi

    if [ -n "$swift_bin" ] && [ -f "$swift_bin" ]; then
        cp "$swift_bin" "$macos_dir/KocortApp"
        ok "Swift binary copied from: $swift_bin"
    else
        fail "Swift binary not found after build. Searched in $swift_dir/.build/"
    fi

    # 3. Build and embed the Go kocort binary
    cd "$PROJECT_ROOT"
    if [ "$arch" = "universal" ]; then
        build_macos_universal_binary
        local go_bin="$DIST_DIR/macos_universal/kocort"
    else
        build_macos_go_binary "$arch"
        local go_bin="$DIST_DIR/macos_${arch}/kocort"
    fi
    if [ ! -f "$go_bin" ]; then
        fail "Go binary not found at $go_bin"
    fi
    cp "$go_bin" "$resources/kocort"
    chmod +x "$resources/kocort"

    # 4. Embed external tool binaries (rg, fd)
    embed_tool_bins_macos

    # 5. Embed Playwright driver (if requested)
    embed_playwright_driver_macos

    # 6. Compile Asset Catalog and embed icons
    #    Modern macOS (Big Sur+) reads icons from Assets.car compiled by actool.
    #    This is the same process Xcode uses, and is required for App Store submission.
    local xcassets_dir="$DESKTOP_DIR/macos/KocortApp/Resources/Assets.xcassets"
    if [ -d "$xcassets_dir" ] && command -v actool &>/dev/null; then
        info "Compiling Asset Catalog with actool..."
        local actool_tmp="$DIST_DIR/actool-output"
        mkdir -p "$actool_tmp"
        actool --compile "$resources" \
            --platform macosx \
            --minimum-deployment-target 13.0 \
            --app-icon AppIcon \
            --output-partial-info-plist "$actool_tmp/assetcatalog_generated_info.plist" \
            "$xcassets_dir" 2>&1 | tail -5
        # Merge actool-generated plist entries (CFBundleIconFile, CFBundleIconName)
        if [ -f "$actool_tmp/assetcatalog_generated_info.plist" ]; then
            /usr/libexec/PlistBuddy -c "Merge $actool_tmp/assetcatalog_generated_info.plist" "$contents/Info.plist" 2>/dev/null || true
        fi
        rm -rf "$actool_tmp"
        ok "Asset Catalog compiled → Assets.car + AppIcon.icns"
    elif [ -f "$ICONS_DIR/icon.icns" ]; then
        # Fallback: use pre-built .icns
        warn "actool not found or no Asset Catalog, falling back to .icns"
        cp "$ICONS_DIR/icon.icns" "$resources/AppIcon.icns"
        /usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string AppIcon" "$contents/Info.plist" 2>/dev/null || \
        /usr/libexec/PlistBuddy -c "Set :CFBundleIconFile AppIcon" "$contents/Info.plist" 2>/dev/null || true
    elif [ -f "$ICONS_DIR/icon.png" ]; then
        warn "No .icns or Asset Catalog found, generating .icns from icon.png"
        if command -v iconutil &>/dev/null; then
            local iconset="$DIST_DIR/icon.iconset"
            mkdir -p "$iconset"
            for size in 16 32 128 256 512; do
                sips -z $size $size "$ICONS_DIR/icon.png" --out "$iconset/icon_${size}x${size}.png" 2>/dev/null
                local size2=$((size * 2))
                if [ $size2 -le 1024 ]; then
                    sips -z $size2 $size2 "$ICONS_DIR/icon.png" --out "$iconset/icon_${size}x${size}@2x.png" 2>/dev/null
                fi
            done
            iconutil -c icns "$iconset" -o "$resources/AppIcon.icns" 2>/dev/null && \
                /usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string AppIcon" "$contents/Info.plist" 2>/dev/null || true
            rm -rf "$iconset"
        fi
    fi

    # Copy tray template icons (macOS auto-adapts light/dark via "Template" suffix)
    for trayFile in trayTemplate.png "trayTemplate@2x.png" tray.png "tray@2x.png"; do
        if [ -f "$ICONS_DIR/$trayFile" ]; then
            cp "$ICONS_DIR/$trayFile" "$resources/$trayFile"
        fi
    done

    ok "macOS .app bundle created: $app_dir"
    ls -la "$macos_dir/" >&2
    ls -la "$resources/" >&2

    echo ""
    info "To test: open $app_dir"
}

# ---------- embed Playwright driver into app bundle ----------
embed_playwright_driver_macos() {
    local app_dir="$DIST_DIR/Kocort.app"
    local resources="$app_dir/Contents/Resources"
    local dest_dir="$resources/playwright-driver"

    if [ -z "$WITH_BROWSER" ]; then
        info "Skipping Playwright driver bundling (use --with-browser or --with-chromium)"
        return 0
    fi

    # Check for pre-built driver in dist/
    local platform_label
    platform_label="$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/')"
    local pre_built_dirs=(
        "$DIST_DIR/$platform_label/playwright-driver"
        "$DIST_DIR/playwright-driver"
        "$PROJECT_ROOT/playwright-driver"
    )
    local pre_built=""
    for d in "${pre_built_dirs[@]}"; do
        if [ -d "$d" ]; then
            pre_built="$d"
            break
        fi
    done

    if [ -n "$pre_built" ]; then
        info "Using pre-built Playwright driver from: $pre_built"
        cp -R "$pre_built" "$dest_dir"
    else
        info "Downloading Playwright driver (mode: $WITH_BROWSER)..."
        local bundle_flags=""
        if [ "$WITH_BROWSER" = "chromium" ]; then
            bundle_flags="--with-chromium"
        fi
        # Use the existing bundle-browser.sh script
        KOCORT_BROWSER_DIST="$DIST_DIR" "$SCRIPT_DIR/bundle-browser.sh" $bundle_flags

        # Find the downloaded driver
        local downloaded="$DIST_DIR/$platform_label/playwright-driver"
        if [ ! -d "$downloaded" ]; then
            downloaded="$DIST_DIR/playwright-driver"
        fi
        if [ ! -d "$downloaded" ]; then
            fail "Playwright driver not found after bundle-browser.sh"
        fi
        cp -R "$downloaded" "$dest_dir"
    fi

    local driver_size
    driver_size=$(du -sh "$dest_dir" | awk '{print $1}')
    ok "Playwright driver embedded: $dest_dir ($driver_size)"
}

# ---------- embed external tool binaries into app bundle ----------
embed_tool_bins_macos() {
    local app_dir="$DIST_DIR/Kocort.app"
    local resources="$app_dir/Contents/Resources"
    local dest_dir="$resources/bin/tools"
    mkdir -p "$dest_dir"

    local arch_list=("darwin_universal" "darwin_arm64" "darwin_amd64")

    for tool_name in "${TOOL_BINS_NAMES[@]}"; do
        local found=""
        for arch_dir in "${arch_list[@]}"; do
            local src="$TOOL_BINS_DIR/$arch_dir/$tool_name"
            if [ -f "$src" ]; then
                found="$src"
                break
            fi
        done
        if [ -z "$found" ]; then
            # Fallback: check flat bin/tools/<name>
            if [ -f "$TOOL_BINS_DIR/$tool_name" ]; then
                found="$TOOL_BINS_DIR/$tool_name"
            fi
        fi
        if [ -z "$found" ]; then
            # Last resort: check system PATH
            if command -v "$tool_name" &>/dev/null; then
                found="$(command -v "$tool_name")"
                warn "Using system-installed $tool_name from $found"
            fi
        fi
        if [ -n "$found" ]; then
            cp "$found" "$dest_dir/$tool_name"
            chmod +x "$dest_dir/$tool_name"
            ok "Embedded tool binary: $tool_name (from $found)"
        else
            warn "Tool binary not found: $tool_name — grep/find will use pure-Go fallback"
        fi
    done
}

embed_tool_bins_windows() {
    local out_dir="$1"
    local dest_dir="$out_dir/bin/tools"
    mkdir -p "$dest_dir"

    for tool_name in "${TOOL_BINS_NAMES[@]}"; do
        local found=""
        for arch_dir in "windows_amd64" "windows_arm64"; do
            local src="$TOOL_BINS_DIR/$arch_dir/${tool_name}.exe"
            if [ -f "$src" ]; then
                found="$src"
                break
            fi
        done
        if [ -z "$found" ] && [ -f "$TOOL_BINS_DIR/${tool_name}.exe" ]; then
            found="$TOOL_BINS_DIR/${tool_name}.exe"
        fi
        if [ -n "$found" ]; then
            cp "$found" "$dest_dir/${tool_name}.exe"
            ok "Embedded tool binary: ${tool_name}.exe (from $found)"
        else
            warn "Tool binary not found: ${tool_name}.exe — grep/find will use pure-Go fallback"
        fi
    done
}

# ---------- macOS code signing ----------
codesign_app() {
    local app_dir="$DIST_DIR/Kocort.app"
    local identity="${KOCORT_CODESIGN_IDENTITY:--}"

    info "Code signing with identity: $identity"

    # Sign embedded tool binaries first (innermost → outermost)
    local tools_dir="$app_dir/Contents/Resources/bin/tools"
    if [ -d "$tools_dir" ]; then
        for tool_bin in "$tools_dir"/*; do
            if [ -f "$tool_bin" ] && [ -x "$tool_bin" ]; then
                info "Signing tool binary: $(basename "$tool_bin")"
                codesign --force --options runtime \
                    --sign "$identity" \
                    "$tool_bin"
            fi
        done
    fi

    # Sign Playwright driver binaries (node, ffmpeg, chromium, etc.)
    local pw_dir="$app_dir/Contents/Resources/playwright-driver"
    if [ -d "$pw_dir" ]; then
        info "Signing Playwright driver binaries..."
        # Find all Mach-O executables and dylibs in the driver directory
        find "$pw_dir" -type f \( -perm +111 -o -name '*.dylib' -o -name '*.so' \) | while read -r pw_bin; do
            # Check if it's actually a Mach-O file
            if file "$pw_bin" | grep -q 'Mach-O'; then
                info "  Signing: ${pw_bin#$app_dir/}"
                codesign --force --options runtime \
                    --sign "$identity" \
                    "$pw_bin" 2>/dev/null || warn "  Failed to sign: $(basename "$pw_bin")"
            fi
        done
        ok "Playwright driver signing complete"
    fi

    # Sign the embedded Go binary
    codesign --force --options runtime \
        --sign "$identity" \
        "$app_dir/Contents/Resources/kocort"

    # Sign the main app
    local entitlements="$DESKTOP_DIR/macos/KocortApp/KocortApp-direct.entitlements"
    if [ "${KOCORT_APP_STORE:-0}" = "1" ]; then
        entitlements="$DESKTOP_DIR/macos/KocortApp/KocortApp.entitlements"
    fi

    codesign --force --options runtime \
        --entitlements "$entitlements" \
        --sign "$identity" \
        "$app_dir"

    ok "Code signing complete"
    codesign -dvvv "$app_dir" 2>&1 | head -5
}

# ---------- macOS DMG ----------
build_macos_dmg() {
    local app_dir="$DIST_DIR/Kocort.app"
    local dmg_path="$DIST_DIR/Kocort-${VERSION}.dmg"

    if [ ! -d "$app_dir" ]; then
        build_macos_app_bundle
    fi

    if command -v create-dmg &>/dev/null; then
        info "Creating DMG with create-dmg..."
        create-dmg \
            --volname "Kocort" \
            --volicon "$ICONS_DIR/icon.icns" \
            --window-pos 200 120 \
            --window-size 600 400 \
            --icon-size 100 \
            --icon "Kocort.app" 150 185 \
            --app-drop-link 450 185 \
            --hide-extension "Kocort.app" \
            "$dmg_path" \
            "$app_dir" \
            2>/dev/null || true

        if [ -f "$dmg_path" ]; then
            ok "DMG created: $dmg_path"
            return 0
        fi
    fi

    # Fallback: simple DMG creation
    info "Creating simple DMG..."
    local tmp_dmg="$DIST_DIR/tmp_kocort.dmg"
    local mount_point="/tmp/kocort_dmg_$$"

    hdiutil create -size 200m -fs HFS+ -volname "Kocort" "$tmp_dmg" -ov
    hdiutil attach "$tmp_dmg" -mountpoint "$mount_point"
    cp -R "$app_dir" "$mount_point/"
    ln -s /Applications "$mount_point/Applications"
    hdiutil detach "$mount_point"
    hdiutil convert "$tmp_dmg" -format UDZO -o "$dmg_path"
    rm -f "$tmp_dmg"

    ok "DMG created: $dmg_path"
    ls -lh "$dmg_path"
}

# ---------- macOS notarization ----------
notarize_app() {
    local dmg_path="$DIST_DIR/Kocort-${VERSION}.dmg"
    local apple_id="${KOCORT_APPLE_ID:-}"
    local team_id="${KOCORT_TEAM_ID:-}"
    local password="${KOCORT_NOTARY_PASSWORD:-}"

    if [ -z "$apple_id" ] || [ -z "$team_id" ] || [ -z "$password" ]; then
        warn "Skipping notarization. Set KOCORT_APPLE_ID, KOCORT_TEAM_ID, KOCORT_NOTARY_PASSWORD"
        return 0
    fi

    info "Submitting for notarization..."
    xcrun notarytool submit "$dmg_path" \
        --apple-id "$apple_id" \
        --team-id "$team_id" \
        --password "$password" \
        --wait

    info "Stapling notarization ticket..."
    xcrun stapler staple "$dmg_path"

    ok "Notarization complete"
}

# ---------- usage ----------
usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Build kocort desktop editions (system tray / menubar).

Options:
    (no flags)        Build for current platform
    --linux           Build Linux tray binary (amd64)
  --windows         Build Windows tray .exe (amd64)
  --windows-arm     Build Windows tray .exe (arm64)
    --macos           Build macOS .app bundle (native arch)
    --macos-universal Build macOS .app bundle (universal)
  --macos-sign      Build + code sign macOS .app
  --macos-dmg       Build macOS .dmg installer
  --macos-notarize  Build + sign + notarize macOS .dmg
  --no-sign         Skip code signing (produce unsigned .app for manual signing)
  --with-browser    Also bundle Playwright driver (system Chrome/Edge required)
  --with-chromium   Also bundle Playwright driver + Chromium (fully offline)
  --all             Build all platforms
  --help            Show this help

Environment:
  KOCORT_VERSION              Version string (default: git describe)
  KOCORT_WITH_BROWSER         Bundle browser driver: "driver" or "chromium"
  KOCORT_BUNDLE_ID            macOS bundle identifier (default: com.kocort.app)
  KOCORT_CODESIGN_IDENTITY    macOS signing identity (default: ad-hoc "-")
  KOCORT_NO_SIGN              Set to "1" to skip signing (same as --no-sign)
  KOCORT_APPLE_ID             Apple ID for notarization
  KOCORT_TEAM_ID              Apple Team ID for notarization
  KOCORT_NOTARY_PASSWORD      App-specific password for notarization
  KOCORT_APP_STORE            Set to "1" to use App Store entitlements

Examples:
    # Build Linux desktop binary for Ubuntu
    ./scripts/build-desktop.sh --linux

  # Build Windows .exe with tray icon
  ./scripts/build-desktop.sh --windows

  # Build macOS .app bundle
  ./scripts/build-desktop.sh --macos

  # Full macOS release (sign + DMG + notarize)
  KOCORT_CODESIGN_IDENTITY="Developer ID Application: ..." \\
  KOCORT_APPLE_ID="dev@example.com" \\
  KOCORT_TEAM_ID="ABCDE12345" \\
  KOCORT_NOTARY_PASSWORD="xxxx-xxxx-xxxx-xxxx" \\
  ./scripts/build-desktop.sh --macos-notarize

  # Build macOS .app with bundled browser driver (uses system Chrome)
  ./scripts/build-desktop.sh --macos --with-browser

  # Build macOS .app with bundled Chromium (fully offline, ~200MB larger)
  ./scripts/build-desktop.sh --macos --with-chromium

  # Build everything
  ./scripts/build-desktop.sh --all
EOF
}

# ---------- main ----------
main() {
    local action=""

    while [ $# -gt 0 ]; do
        case "$1" in
            --linux)           action="linux";         shift ;;
            --windows)         action="windows";       shift ;;
            --windows-arm)     action="windows-arm";   shift ;;
            --macos)           action="macos";         shift ;;
            --macos-universal) action="macos-universal"; shift ;;
            --macos-sign)      action="macos-sign";    shift ;;
            --macos-dmg)       action="macos-dmg";     shift ;;
            --macos-notarize)  action="macos-notarize"; shift ;;
            --with-browser)    WITH_BROWSER="driver";  shift ;;
            --with-chromium)   WITH_BROWSER="chromium"; shift ;;
            --no-sign)         SKIP_SIGN=1;             shift ;;
            --all)             action="all";           shift ;;
            --help|-h)         usage; exit 0 ;;
            *) fail "Unknown option: $1. Use --help for usage." ;;
        esac
    done

    info "kocort desktop build — version $VERSION ($COMMIT)"
    echo ""

    case "${action:-auto}" in
        linux)
            build_linux "amd64"
            ;;
        windows)
            build_windows "amd64"
            ;;
        windows-arm)
            build_windows "arm64"
            ;;
        macos)
            build_macos_app_bundle "$(uname -m)"
            ;;
        macos-universal)
            build_macos_app_bundle "universal"
            ;;
        macos-sign)
            build_macos_app_bundle "universal"
            if [ "$SKIP_SIGN" = "1" ]; then
                warn "Skipping code signing (--no-sign). You can sign later with:"
                warn "  codesign --deep --force --options runtime --sign \"Developer ID Application: ...\" $DIST_DIR/Kocort.app"
            else
                codesign_app
            fi
            ;;
        macos-dmg)
            build_macos_app_bundle "universal"
            if [ "$SKIP_SIGN" != "1" ]; then
                codesign_app
            else
                warn "Skipping code signing (--no-sign). DMG will contain unsigned app."
            fi
            build_macos_dmg
            ;;
        macos-notarize)
            build_macos_app_bundle "universal"
            codesign_app
            build_macos_dmg
            notarize_app
            ;;
        all)
            if [ "$(uname -s)" = "Linux" ]; then
                build_linux "$(uname -m)"
            else
                warn "Skipping Linux desktop build in --all: requires a native Linux/Ubuntu host"
            fi
            build_windows "amd64"
            build_windows "arm64"
            build_macos_app_bundle "universal"
            codesign_app
            build_macos_dmg
            echo ""
            ok "All desktop builds complete. Output in $DIST_DIR/"
            ls -la "$DIST_DIR"/
            ;;
        auto)
            # Auto-detect current platform
            case "$(uname -s)" in
                Darwin)
                    build_macos_app_bundle "$(uname -m)"
                    ;;
                Linux)
                    build_linux "$(uname -m)"
                    ;;
                MINGW*|MSYS*|CYGWIN*)
                    build_windows "amd64"
                    ;;
                *)
                    fail "Unsupported platform: $(uname -s)"
                    ;;
            esac
            ;;
    esac
}

main "$@"
