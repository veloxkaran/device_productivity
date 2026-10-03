#!/bin/bash
# My Monitor — macOS .dmg packager
# Produces a drag-to-install .dmg with a proper .app bundle.
# Builds for the native machine architecture (arm64 on Apple Silicon, amd64 on Intel).
# Pass ARCH=universal to attempt a universal binary (requires Xcode + arm64+amd64 CGo cross-compile).
#
# Usage:
#   ./build-dmg.sh             # native arch
#   ARCH=universal ./build-dmg.sh  # universal (Intel + Apple Silicon)

set -euo pipefail

# ── Config ────────────────────────────────────────────────────────
APP_NAME="MyMonitor"
DISPLAY_NAME="My Monitor"
BINARY_NAME="my-monitor"
# Single source of truth: the AppVersion the agent reports (cloud/heartbeat.go).
VERSION="${VERSION:-$(sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' "$(dirname "${BASH_SOURCE[0]}")/cloud/heartbeat.go")}"
: "${VERSION:?could not read AppVersion from cloud/heartbeat.go}"
BUNDLE_ID="com.hajir.tracker"
SIGN_IDENTITY="${SIGN_IDENTITY:-}"
if [[ -z "$SIGN_IDENTITY" ]] && security find-identity -v -p codesigning 2>/dev/null | grep -q "Hajir Local Signing"; then
    SIGN_IDENTITY="Hajir Local Signing"
fi
PORT=8090
HAJIR_API_URL="${HAJIR_API_URL:-http://localhost:8001/api/v2}"
HUB_URL="${HUB_URL:-http://localhost:4010}"
# Minimum macOS the built app will install on. 10.15 (Catalina) is the floor for a
# Go 1.21+ toolchain. Build with Go 1.20 and MIN_MACOS=10.13 to reach High Sierra / Mojave.
MIN_MACOS="${MIN_MACOS:-10.15}"
LDFLAGS="-s -w -X my-monitor/web.DefaultHajirAPIURL=${HAJIR_API_URL} -X my-monitor/web.DefaultHubURL=${HUB_URL}"
# Legacy macOS (10.13/10.14): run this with a Go 1.20 toolchain and GO_TAGS=legacyos
# MIN_MACOS=10.13, e.g.:  GO_TAGS=legacyos MIN_MACOS=10.13 ARCH=universal bash build-dmg.sh
# The legacyos tag drops the hub server (Go 1.22-only) from the app and puts it on the
# "-legacy" auto-update lane; the min/max shim covers the builtins Go 1.20 lacks.
GO_TAGS="${GO_TAGS:-}"
GO_TAGS_FLAG=""
[ -n "$GO_TAGS" ] && GO_TAGS_FLAG="-tags $GO_TAGS"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/dist"
APP_BUNDLE="${BUILD_DIR}/${APP_NAME}.app"
DMG_NAME="${APP_NAME}-${VERSION}.dmg"

# ── Detect arch ───────────────────────────────────────────────────
HOST_ARCH=$(uname -m)   # arm64 or x86_64
WANT_ARCH="${ARCH:-native}"

if [[ "$WANT_ARCH" == "native" ]]; then
    GOARCH="$([[ "$HOST_ARCH" == "arm64" ]] && echo arm64 || echo amd64)"
    BUILD_MODE="native ($GOARCH)"
else
    BUILD_MODE="universal"
fi

# ── Helpers ───────────────────────────────────────────────────────
step()  { echo -e "\n\033[1;36m▶  $1\033[0m"; }
ok()    { echo -e "   \033[0;32m✓\033[0m  $1"; }
warn()  { echo -e "   \033[1;33m⚠\033[0m  $1"; }
info()  { echo -e "   \033[2mℹ  $1\033[0m"; }

# Pin the Mach-O minimum OS for EVERY slice. Without this, cgo inherits the host SDK
# (e.g. macOS 26) and the app shows a "no entry" icon / "update macOS" on older Macs.
export MACOSX_DEPLOYMENT_TARGET="${MIN_MACOS}"
export CGO_ENABLED=1

# ── Build binary ─────────────────────────────────────────────────
step "Building Go binary — $BUILD_MODE"

rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

cd "$SCRIPT_DIR"
if [[ "$GO_TAGS" == *legacyos* ]]; then
    # Go 1.20 can't tidy or read a "go 1.22" directive; pin it for this build and restore.
    cp go.mod go.mod.legacybak
    trap 'mv -f go.mod.legacybak go.mod 2>/dev/null || true' EXIT
    sed -i.sedbak 's/^go 1\.22$/go 1.20/' go.mod && rm -f go.mod.sedbak
else
    go mod tidy
fi

if [[ "$BUILD_MODE" == "universal" ]]; then
    # Universal binary requires CGo cross-compile for the non-native arch.
    # clang on Apple Silicon can target x86_64 with -arch flag.
    info "Building arm64..."
    CGO_CFLAGS="-arch arm64 -mmacosx-version-min=${MIN_MACOS}" \
    CGO_LDFLAGS="-arch arm64 -mmacosx-version-min=${MIN_MACOS}" \
    CC="clang -arch arm64" \
    GOARCH=arm64 GOOS=darwin go build ${GO_TAGS_FLAG} -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY_NAME}-arm64" .

    info "Building amd64 (CGo cross-compile via clang -arch x86_64)..."
    # -mmacosx-version-min pins the Mach-O's minimum OS to match the plist, so the
    # installed app runs on ${MIN_MACOS} and up rather than demanding the host's SDK.
    CGO_ENABLED=1 \
    MACOSX_DEPLOYMENT_TARGET="${MIN_MACOS}" \
    CGO_CFLAGS="-arch x86_64 -mmacosx-version-min=${MIN_MACOS}" \
    CGO_LDFLAGS="-arch x86_64 -mmacosx-version-min=${MIN_MACOS}" \
    CC="clang -arch x86_64" \
    GOARCH=amd64 GOOS=darwin \
    go build ${GO_TAGS_FLAG} -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY_NAME}-amd64" .

    lipo -create -output "${BUILD_DIR}/${BINARY_NAME}" \
        "${BUILD_DIR}/${BINARY_NAME}-arm64" \
        "${BUILD_DIR}/${BINARY_NAME}-amd64"
    rm "${BUILD_DIR}/${BINARY_NAME}-arm64" "${BUILD_DIR}/${BINARY_NAME}-amd64"
    ok "Universal binary: ${BUILD_DIR}/${BINARY_NAME}"
else
    CGO_CFLAGS="-mmacosx-version-min=${MIN_MACOS}" \
    CGO_LDFLAGS="-mmacosx-version-min=${MIN_MACOS}" \
    GOARCH="$GOARCH" GOOS=darwin go build ${GO_TAGS_FLAG} -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY_NAME}" .
    ok "Binary (${GOARCH}): ${BUILD_DIR}/${BINARY_NAME}"
fi

# Guard: fail the build if any slice still demands a newer macOS than MIN_MACOS.
if command -v vtool >/dev/null 2>&1; then
    for a in $(lipo -archs "${BUILD_DIR}/${BINARY_NAME}"); do
        MINOS=$(vtool -arch "$a" -show-build "${BUILD_DIR}/${BINARY_NAME}" 2>/dev/null | awk '/minos/{print $2; exit}')
        info "$a minos: ${MINOS:-unknown}"
        if [[ -n "$MINOS" ]] && [[ "$(printf '%s\n%s\n' "$MINOS" "$MIN_MACOS" | sort -V | tail -1)" != "$MIN_MACOS" ]] \
           && [[ "$a" != "arm64" || "${MINOS%%.*}" -gt 11 ]]; then
            echo "✗ $a slice requires macOS $MINOS (> $MIN_MACOS). Aborting." >&2; exit 1
        fi
    done
fi

# ── Build .app bundle ─────────────────────────────────────────────
step "Creating .app bundle"

mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"

# Main executable — the Go binary itself (no shell launcher), so macOS
# attributes Screen Recording / Accessibility to this exact app.
cp "${BUILD_DIR}/${BINARY_NAME}" "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}"
chmod +x "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}"

# Menu-bar icon: Hajir stopwatch icon only
cp web/assets/hajir-menubar.png "${APP_BUNDLE}/Contents/Resources/hajir-menubar.png"

# App icon (Finder, Dock, DMG)
cp web/assets/icons/hajir.icns "${APP_BUNDLE}/Contents/Resources/AppIcon.icns"

# Info.plist
cat > "${APP_BUNDLE}/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>      <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>      <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>            <string>${DISPLAY_NAME}</string>
    <key>CFBundleDisplayName</key>     <string>${DISPLAY_NAME}</string>
    <key>CFBundleVersion</key>         <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key><string>${VERSION}</string>
    <key>CFBundlePackageType</key>     <string>APPL</string>
    <key>CFBundleIconFile</key>        <string>AppIcon</string>
    <key>CFBundleSignature</key>       <string>????</string>
    <key>LSMinimumSystemVersion</key>  <string>${MIN_MACOS}</string>
    <key>LSUIElement</key>             <true/>
    <key>NSAppTransportSecurity</key>
    <dict><key>NSAllowsLocalNetworking</key><true/></dict>
    <key>NSScreenCaptureUsageDescription</key><string>Hajir Tracker takes periodic work screenshots while you are tracking time.</string>
    <key>NSHighResolutionCapable</key> <true/>
</dict>
</plist>
PLIST

# Code signing — a stable identity keeps macOS permissions across rebuilds/updates
if [[ -n "$SIGN_IDENTITY" ]]; then
    if [[ "$SIGN_IDENTITY" == Developer\ ID* ]]; then
        codesign --force --deep --options runtime --timestamp --identifier "${BUNDLE_ID}" --sign "$SIGN_IDENTITY" "${APP_BUNDLE}"
    else
        codesign --force --deep --identifier "${BUNDLE_ID}" --sign "$SIGN_IDENTITY" "${APP_BUNDLE}"
    fi
    ok "Signed with: $SIGN_IDENTITY"
else
    codesign --force --deep --sign - --identifier "${BUNDLE_ID}" "${APP_BUNDLE}" && warn "Ad-hoc signed (no SIGN_IDENTITY). macOS will re-ask for permissions after each rebuild; run 'make dev-cert' once to avoid this."
fi

ok ".app bundle: ${APP_BUNDLE}"

# ── Create DMG ────────────────────────────────────────────────────
step "Creating .dmg"

DMG_STAGING="${BUILD_DIR}/.dmg-staging"
TEMP_DMG="${BUILD_DIR}/temp.dmg"
FINAL_DMG="${BUILD_DIR}/${DMG_NAME}"

rm -rf "$DMG_STAGING"
mkdir -p "$DMG_STAGING"

cp -r "${APP_BUNDLE}" "${DMG_STAGING}/"

# Symlink to /Applications so users get the drag-to-install UI
ln -s /Applications "${DMG_STAGING}/Applications"

# Add an uninstaller the user can double-click (native dialogs via osascript)
cat > "${DMG_STAGING}/Uninstall MyMonitor.command" << 'UNINST'
#!/bin/bash
# Uninstalls My Monitor from this Mac using native macOS dialogs.

osa() { /usr/bin/osascript "$@"; }

# 1. Confirm
if ! osa -e 'display dialog "This will remove My Monitor from this Mac, including its background login item." with title "Uninstall My Monitor" buttons {"Cancel","Uninstall"} default button "Uninstall" cancel button "Cancel" with icon caution' >/dev/null 2>&1; then
  exit 0
fi

# 2. Stop login item + running app, remove the app
launchctl unload "$HOME/Library/LaunchAgents/com.hajir.tracker.plist" 2>/dev/null || true
rm -f "$HOME/Library/LaunchAgents/com.hajir.tracker.plist"
pkill -f "MyMonitor.app/Contents/MacOS/MyMonitor" 2>/dev/null || true
pkill -f "/Applications/MyMonitor.app" 2>/dev/null || true
rm -rf "/Applications/MyMonitor.app"

# 3. Ask about local data
DATA_MSG="My Monitor has been removed."
if osa -e 'display dialog "Also delete local data (screenshots, database and settings)?" with title "Uninstall My Monitor" buttons {"Keep Data","Delete Data"} default button "Keep Data" with icon caution' -e 'button returned of result' 2>/dev/null | grep -q "Delete Data"; then
  rm -rf "$HOME/Library/Application Support/MyMonitor"
  rm -f "$HOME/Library/Logs/MyMonitor.log"
  DATA_MSG="My Monitor and its local data have been removed."
fi

# 4. Done
osa -e "display dialog \"${DATA_MSG}\nIf this device was managed, remember to revoke it in the dashboard.\" with title \"Uninstall My Monitor\" buttons {\"OK\"} default button \"OK\" with icon note" >/dev/null 2>&1

# Close the Terminal window this script opened.
osa -e 'tell application "Terminal" to close (every window whose name contains "Uninstall MyMonitor")' >/dev/null 2>&1 &
exit 0
UNINST
chmod +x "${DMG_STAGING}/Uninstall MyMonitor.command"

# Add a README
cat > "${DMG_STAGING}/README.txt" << README
My Monitor v${VERSION}
──────────────────────
1. Drag MyMonitor.app to the Applications folder.
2. Double-click MyMonitor in Applications to start.
3. Your browser will open http://localhost:${PORT} automatically.
4. Default login: admin / admin
   ⚠ Change your password in the Setup wizard!

To uninstall: double-click "Uninstall MyMonitor.command" in this disk image
(or just delete MyMonitor.app from Applications).
Data is stored in ~/Library/Application Support/MyMonitor/
README

# Build a writable DMG first, then convert to compressed read-only
hdiutil create \
    -volname "${DISPLAY_NAME} ${VERSION}" \
    -srcfolder "${DMG_STAGING}" \
    -ov -format UDRW \
    "${TEMP_DMG}" >/dev/null

hdiutil convert "${TEMP_DMG}" \
    -format UDZO \
    -imagekey zlib-level=9 \
    -o "${FINAL_DMG}" >/dev/null

rm -f "${TEMP_DMG}"
rm -rf "${DMG_STAGING}"

ok "DMG: ${FINAL_DMG}"

# ── Notarization (optional) ────────────────────────────────────────
# Runs only when the app was signed with a Developer ID AND credentials are
# provided. Provide EITHER a stored notarytool keychain profile:
#     export AC_NOTARY_PROFILE="hajir-notary"
#   (create once: xcrun notarytool store-credentials hajir-notary \
#        --apple-id you@apple.com --team-id TEAMID --password APP_SPECIFIC_PASSWORD)
# OR the raw credentials:
#     export APPLE_ID="you@apple.com" APPLE_TEAM_ID="TEAMID" APPLE_APP_PASSWORD="app-specific-pw"
if [[ "$SIGN_IDENTITY" == Developer\ ID* ]]; then
    NOTARY_ARGS=()
    if [[ -n "${AC_NOTARY_PROFILE:-}" ]]; then
        NOTARY_ARGS=(--keychain-profile "$AC_NOTARY_PROFILE")
    elif [[ -n "${APPLE_ID:-}" && -n "${APPLE_TEAM_ID:-}" && -n "${APPLE_APP_PASSWORD:-}" ]]; then
        NOTARY_ARGS=(--apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD")
    fi
    if [[ ${#NOTARY_ARGS[@]} -gt 0 ]]; then
        echo "  Submitting to Apple notary service (this can take a few minutes)..."
        if xcrun notarytool submit "${FINAL_DMG}" "${NOTARY_ARGS[@]}" --wait; then
            xcrun stapler staple "${FINAL_DMG}" && ok "Notarized & stapled: ${FINAL_DMG}"
        else
            warn "Notarization failed. The DMG is signed but not notarized (Gatekeeper will warn)."
            warn "Check the log: xcrun notarytool log <submission-id> ${NOTARY_ARGS[*]}"
        fi
    else
        warn "Signed with Developer ID but no notary credentials set — skipping notarization."
        warn "Set AC_NOTARY_PROFILE, or APPLE_ID + APPLE_TEAM_ID + APPLE_APP_PASSWORD, to notarize."
    fi
else
    [[ -n "$SIGN_IDENTITY" ]] && warn "Not a Developer ID signature — cannot notarize (Gatekeeper will still warn on other Macs)."
fi

# ── Done ──────────────────────────────────────────────────────────
echo ""
echo "  ┌────────────────────────────────────────────────────────┐"
echo "  │  Build complete!                                        │"
echo "  │                                                         │"
printf  "  │  %-55s│\n" "DMG: dist/${DMG_NAME}"
echo "  │                                                         │"
echo "  │  How to install:                                        │"
echo "  │    1. Open the .dmg                                     │"
echo "  │    2. Drag MyMonitor.app → Applications                 │"
echo "  │    3. Double-click MyMonitor.app to start               │"
printf  "  │    4. Visit http://localhost:%-28s│\n" "${PORT}"
echo "  └────────────────────────────────────────────────────────┘"
echo ""
