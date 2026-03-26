#!/bin/bash
# =============================================================================
# macOS Code Signing, Notarization & App Store Upload Script
# =============================================================================
#
# This script handles everything AFTER building the unsigned .app bundle:
#   1. Code signing (Developer ID or App Store)
#   2. Verification
#   3. DMG packaging
#   4. Notarization (for direct distribution)
#   5. App Store upload (for Mac App Store)
#
# Prerequisites:
#   - Unsigned .app built with: ./scripts/build-desktop.sh --macos --no-sign
#   - Valid Apple Developer certificate in Keychain
#   - For notarization: App-specific password from appleid.apple.com
#   - For App Store upload: Xcode CLI tools, valid provisioning profile
#
# Usage:
#   # Direct distribution (DMG + notarize)
#   ./scripts/sign-and-release.sh direct
#
#   # App Store (sign with sandbox + upload)
#   ./scripts/sign-and-release.sh appstore
#
#   # Just sign (no packaging/upload)
#   ./scripts/sign-and-release.sh sign-only
#
#   # Verify an existing .app
#   ./scripts/sign-and-release.sh verify
#
# Environment Variables:
#   Required:
#     DEVELOPER_ID        Signing identity for direct distribution
#                         e.g. "Developer ID Application: Your Name (TEAMID)"
#     --- OR ---
#     APP_STORE_ID        Signing identity for App Store
#                         e.g. "3rd Party Mac Developer Application: Your Name (TEAMID)"
#     INSTALLER_ID        Installer signing identity for App Store .pkg
#                         e.g. "3rd Party Mac Developer Installer: Your Name (TEAMID)"
#
#   For notarization (direct distribution only):
#     APPLE_ID            Your Apple ID email
#     TEAM_ID             Apple Developer Team ID (10-char)
#     NOTARY_PASSWORD     App-specific password (generate at appleid.apple.com)
#
#   Optional:
#     APP_PATH             Path to .app (default: dist/Kocort.app)
#     BUNDLE_ID            Bundle identifier (default: com.kocort.app)
#     PROVISIONING_PROFILE Path to .provisionprofile for App Store
#
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
DIST_DIR="$PROJECT_ROOT/dist"
DESKTOP_DIR="$PROJECT_ROOT/desktop"

APP_PATH="${APP_PATH:-$DIST_DIR/Kocort.app}"
BUNDLE_ID="${BUNDLE_ID:-com.kocort.app}"

# ---------- colours ----------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
info()  { echo -e "${CYAN}==>${NC} $*"; }
ok()    { echo -e "${GREEN}==>${NC} $*"; }
warn()  { echo -e "${YELLOW}WARNING:${NC} $*"; }
fail()  { echo -e "${RED}ERROR:${NC} $*" >&2; exit 1; }
step()  { echo -e "\n${BOLD}[$1/$TOTAL_STEPS] $2${NC}"; }

# ---------- preflight ----------
preflight_check() {
    if [ ! -d "$APP_PATH" ]; then
        fail "App not found at: $APP_PATH\n  Build it first: ./scripts/build-desktop.sh --macos --no-sign"
    fi

    if ! command -v codesign &>/dev/null; then
        fail "codesign not found. Install Xcode CLI tools: xcode-select --install"
    fi

    info "App: $APP_PATH"
    info "Bundle ID: $BUNDLE_ID"
}

# ---------- deep sign (innermost → outermost) ----------
deep_sign() {
    local identity="$1"
    local entitlements="$2"
    local app="$APP_PATH"

    info "Signing identity: $identity"
    info "Entitlements: $(basename "$entitlements")"

    # 1. Sign all embedded Mach-O binaries in Resources (tools, Go binary, etc.)
    info "Signing embedded binaries..."
    local count=0
    while IFS= read -r -d '' bin; do
        if file "$bin" | grep -q 'Mach-O'; then
            codesign --force --options runtime \
                --sign "$identity" \
                --entitlements "$entitlements" \
                "$bin"
            count=$((count + 1))
        fi
    done < <(find "$app/Contents/Resources" -type f -perm +111 -print0 2>/dev/null)

    # Also sign dylibs/so
    while IFS= read -r -d '' lib; do
        codesign --force --options runtime \
            --sign "$identity" \
            "$lib" 2>/dev/null || true
        count=$((count + 1))
    done < <(find "$app/Contents" -type f \( -name '*.dylib' -o -name '*.so' \) -print0 2>/dev/null)

    info "Signed $count embedded binaries"

    # 2. Sign the main app bundle
    info "Signing app bundle..."
    codesign --force --options runtime \
        --entitlements "$entitlements" \
        --sign "$identity" \
        "$app"

    ok "Code signing complete"
}

# ---------- verify ----------
verify_signature() {
    local app="$APP_PATH"

    info "Verifying code signature..."
    echo ""

    # Basic verification
    if codesign --verify --deep --strict "$app" 2>&1; then
        ok "Signature: VALID"
    else
        fail "Signature: INVALID"
    fi

    # Gatekeeper assessment
    if spctl --assess --type exec "$app" 2>&1; then
        ok "Gatekeeper: ACCEPTED"
    else
        warn "Gatekeeper: REJECTED (expected if not notarized yet)"
    fi

    echo ""
    info "Signature details:"
    codesign -dvvv "$app" 2>&1 | grep -E 'Authority|TeamIdentifier|Identifier|Signature|Info.plist|CDHash' | head -15
    echo ""

    # Check entitlements
    info "Entitlements:"
    codesign -d --entitlements - "$app" 2>/dev/null | head -30 || echo "  (none)"
    echo ""
}

# ---------- DMG ----------
package_dmg() {
    local version
    version=$(cd "$PROJECT_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo "dev")
    local dmg_path="$DIST_DIR/Kocort-${version}.dmg"

    info "Creating DMG: $dmg_path"

    # Remove existing
    rm -f "$dmg_path"

    if command -v create-dmg &>/dev/null; then
        local icon_arg=""
        if [ -f "$DESKTOP_DIR/icons/icon.icns" ]; then
            icon_arg="--volicon $DESKTOP_DIR/icons/icon.icns"
        fi

        create-dmg \
            --volname "Kocort" \
            $icon_arg \
            --window-pos 200 120 \
            --window-size 600 400 \
            --icon-size 100 \
            --icon "Kocort.app" 150 185 \
            --app-drop-link 450 185 \
            --hide-extension "Kocort.app" \
            "$dmg_path" \
            "$APP_PATH" \
            2>/dev/null || true

        if [ -f "$dmg_path" ]; then
            ok "DMG created: $dmg_path ($(du -h "$dmg_path" | cut -f1))"
            echo "$dmg_path"
            return 0
        fi
    fi

    # Fallback: simple DMG
    info "Falling back to hdiutil..."
    local tmp_dmg="$DIST_DIR/tmp_kocort.dmg"
    local mount_point="/tmp/kocort_dmg_$$"

    hdiutil create -size 500m -fs HFS+ -volname "Kocort" "$tmp_dmg" -ov -quiet
    hdiutil attach "$tmp_dmg" -mountpoint "$mount_point" -quiet
    cp -R "$APP_PATH" "$mount_point/"
    ln -s /Applications "$mount_point/Applications"
    hdiutil detach "$mount_point" -quiet
    hdiutil convert "$tmp_dmg" -format UDZO -o "$dmg_path" -quiet
    rm -f "$tmp_dmg"

    ok "DMG created: $dmg_path ($(du -h "$dmg_path" | cut -f1))"
    echo "$dmg_path"
}

# ---------- notarization ----------
notarize() {
    local dmg_path="$1"
    local apple_id="${APPLE_ID:-}"
    local team_id="${TEAM_ID:-}"
    local password="${NOTARY_PASSWORD:-}"

    if [ -z "$apple_id" ] || [ -z "$team_id" ] || [ -z "$password" ]; then
        fail "Notarization requires: APPLE_ID, TEAM_ID, NOTARY_PASSWORD\n\n  Export them before running:\n    export APPLE_ID=\"dev@example.com\"\n    export TEAM_ID=\"ABCDE12345\"\n    export NOTARY_PASSWORD=\"xxxx-xxxx-xxxx-xxxx\""
    fi

    info "Submitting for notarization..."
    info "  Apple ID: $apple_id"
    info "  Team ID:  $team_id"
    info "  File:     $dmg_path"
    echo ""

    xcrun notarytool submit "$dmg_path" \
        --apple-id "$apple_id" \
        --team-id "$team_id" \
        --password "$password" \
        --wait

    info "Stapling notarization ticket..."
    xcrun stapler staple "$dmg_path"

    ok "Notarization complete!"
    ok "Ready to distribute: $dmg_path"
}

# ---------- App Store .pkg ----------
package_appstore_pkg() {
    local installer_id="${INSTALLER_ID:-}"
    if [ -z "$installer_id" ]; then
        fail "INSTALLER_ID required for App Store .pkg\n  e.g. INSTALLER_ID=\"3rd Party Mac Developer Installer: Your Name (TEAMID)\""
    fi

    local version
    version=$(cd "$PROJECT_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo "dev")
    local pkg_path="$DIST_DIR/Kocort-${version}.pkg"

    info "Creating App Store .pkg: $pkg_path"

    # Embed provisioning profile if provided
    if [ -n "${PROVISIONING_PROFILE:-}" ] && [ -f "$PROVISIONING_PROFILE" ]; then
        cp "$PROVISIONING_PROFILE" "$APP_PATH/Contents/embedded.provisionprofile"
        info "Embedded provisioning profile"
    fi

    productbuild \
        --component "$APP_PATH" /Applications \
        --sign "$installer_id" \
        "$pkg_path"

    ok "App Store .pkg created: $pkg_path ($(du -h "$pkg_path" | cut -f1))"
    echo "$pkg_path"
}

# ---------- App Store upload ----------
upload_appstore() {
    local pkg_path="$1"
    local apple_id="${APPLE_ID:-}"
    local password="${NOTARY_PASSWORD:-}"

    if [ -z "$apple_id" ] || [ -z "$password" ]; then
        fail "Upload requires: APPLE_ID, NOTARY_PASSWORD (App-specific password)\n\n  Export them before running:\n    export APPLE_ID=\"dev@example.com\"\n    export NOTARY_PASSWORD=\"xxxx-xxxx-xxxx-xxxx\""
    fi

    info "Uploading to App Store Connect..."
    info "  Apple ID: $apple_id"
    info "  Package:  $pkg_path"
    echo ""

    xcrun altool --upload-app \
        -f "$pkg_path" \
        -t macos \
        -u "$apple_id" \
        -p "$password" \
    || {
        # Try newer API if altool fails
        warn "altool failed, trying notarytool upload..."
        xcrun notarytool submit "$pkg_path" \
            --apple-id "$apple_id" \
            --team-id "${TEAM_ID:-}" \
            --password "$password" \
            --wait
    }

    ok "Upload complete! Check App Store Connect for status."
}

# =============================================================================
# Workflows
# =============================================================================

# Direct distribution: sign → DMG → notarize
workflow_direct() {
    TOTAL_STEPS=4
    local identity="${DEVELOPER_ID:-}"
    if [ -z "$identity" ]; then
        fail "DEVELOPER_ID required.\n\n  Export it:\n    export DEVELOPER_ID=\"Developer ID Application: Your Name (TEAMID)\"\n\n  List available identities:\n    security find-identity -v -p codesigning"
    fi

    local entitlements="$DESKTOP_DIR/macos/KocortApp/KocortApp-direct.entitlements"

    step 1 "Code Signing (Developer ID)"
    deep_sign "$identity" "$entitlements"

    step 2 "Verification"
    verify_signature

    step 3 "Packaging DMG"
    local dmg_path
    dmg_path=$(package_dmg)

    step 4 "Notarization"
    notarize "$dmg_path"

    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  Direct Distribution Complete!${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    echo "  DMG: $dmg_path"
    echo ""
    echo "  Distribute via website, GitHub Releases, Homebrew, etc."
    echo ""
}

# App Store: sign with sandbox → .pkg → upload
workflow_appstore() {
    TOTAL_STEPS=4
    local identity="${APP_STORE_ID:-}"
    if [ -z "$identity" ]; then
        fail "APP_STORE_ID required.\n\n  Export it:\n    export APP_STORE_ID=\"3rd Party Mac Developer Application: Your Name (TEAMID)\"\n    export INSTALLER_ID=\"3rd Party Mac Developer Installer: Your Name (TEAMID)\"\n\n  List available identities:\n    security find-identity -v -p codesigning"
    fi

    local entitlements="$DESKTOP_DIR/macos/KocortApp/KocortApp.entitlements"

    step 1 "Code Signing (App Store + Sandbox)"
    deep_sign "$identity" "$entitlements"

    step 2 "Verification"
    verify_signature

    step 3 "Packaging .pkg"
    local pkg_path
    pkg_path=$(package_appstore_pkg)

    step 4 "Uploading to App Store Connect"
    upload_appstore "$pkg_path"

    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  App Store Upload Complete!${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    echo "  Check status: https://appstoreconnect.apple.com"
    echo ""
}

# Sign only (no packaging)
workflow_sign_only() {
    TOTAL_STEPS=2

    # Auto-detect identity
    local identity=""
    local entitlements=""

    if [ -n "${APP_STORE_ID:-}" ]; then
        identity="$APP_STORE_ID"
        entitlements="$DESKTOP_DIR/macos/KocortApp/KocortApp.entitlements"
        info "Mode: App Store (sandbox)"
    elif [ -n "${DEVELOPER_ID:-}" ]; then
        identity="$DEVELOPER_ID"
        entitlements="$DESKTOP_DIR/macos/KocortApp/KocortApp-direct.entitlements"
        info "Mode: Direct Distribution"
    else
        fail "Set DEVELOPER_ID or APP_STORE_ID.\n\n  List available identities:\n    security find-identity -v -p codesigning"
    fi

    step 1 "Code Signing"
    deep_sign "$identity" "$entitlements"

    step 2 "Verification"
    verify_signature
}

# Verify only
workflow_verify() {
    TOTAL_STEPS=1
    step 1 "Verification"
    verify_signature
}

# =============================================================================
# Usage
# =============================================================================
usage() {
    cat <<'EOF'
Usage: sign-and-release.sh <command>

Commands:
  direct      Sign + DMG + Notarize (for website/GitHub/Homebrew distribution)
  appstore    Sign + .pkg + Upload (for Mac App Store submission)
  sign-only   Just sign the .app (auto-detects identity type)
  verify      Verify an existing .app's signature
  help        Show this help

Quick Start:

  # 1. Build unsigned app
  ./scripts/build-desktop.sh --macos --no-sign

  # 2a. Direct distribution
  export DEVELOPER_ID="Developer ID Application: Your Name (TEAMID)"
  export APPLE_ID="dev@example.com"
  export TEAM_ID="ABCDE12345"
  export NOTARY_PASSWORD="xxxx-xxxx-xxxx-xxxx"
  ./scripts/sign-and-release.sh direct

  # 2b. App Store
  export APP_STORE_ID="3rd Party Mac Developer Application: Your Name (TEAMID)"
  export INSTALLER_ID="3rd Party Mac Developer Installer: Your Name (TEAMID)"
  export APPLE_ID="dev@example.com"
  export NOTARY_PASSWORD="xxxx-xxxx-xxxx-xxxx"
  ./scripts/sign-and-release.sh appstore

  # List your available signing identities
  security find-identity -v -p codesigning

Environment Variables:
  DEVELOPER_ID          Direct distribution signing identity
  APP_STORE_ID          App Store signing identity
  INSTALLER_ID          App Store installer signing identity
  APPLE_ID              Apple ID email (for notarization/upload)
  TEAM_ID               Apple Developer Team ID
  NOTARY_PASSWORD       App-specific password
  APP_PATH              Path to .app (default: dist/Kocort.app)
  BUNDLE_ID             Bundle identifier (default: com.kocort.app)
  PROVISIONING_PROFILE  Path to .provisionprofile for App Store
EOF
}

# ---------- main ----------
main() {
    local command="${1:-help}"

    echo -e "${BOLD}Kocort macOS Sign & Release${NC}"
    echo ""

    preflight_check

    case "$command" in
        direct)      workflow_direct ;;
        appstore)    workflow_appstore ;;
        sign-only)   workflow_sign_only ;;
        verify)      workflow_verify ;;
        help|--help|-h) usage ;;
        *) fail "Unknown command: $command\n  Run with 'help' for usage." ;;
    esac
}

main "$@"
