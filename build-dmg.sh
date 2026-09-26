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
VERSION="2.0.0"
BUNDLE_ID="com.hajir.tracker"
SIGN_IDENTITY="${SIGN_IDENTITY:-}"
if [[ -z "$SIGN_IDENTITY" ]] && security find-identity -v -p codesigning 2>/dev/null | grep -q "Hajir Local Signing"; then
    SIGN_IDENTITY="Hajir Local Signing"
fi
PORT=8090
HAJIR_API_URL="${HAJIR_API_URL:-http://localhost:8001/api/v2}"
HUB_URL="${HUB_URL:-http://localhost:4010}"
LDFLAGS="-s -w -X my-monitor/web.DefaultHajirAPIURL=${HAJIR_API_URL} -X my-monitor/web.DefaultHubURL=${HUB_URL}"

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

# ── Build binary ─────────────────────────────────────────────────
step "Building Go binary — $BUILD_MODE"

rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

cd "$SCRIPT_DIR"
go mod tidy

if [[ "$BUILD_MODE" == "universal" ]]; then
    # Universal binary requires CGo cross-compile for the non-native arch.
    # clang on Apple Silicon can target x86_64 with -arch flag.
    info "Building arm64..."
    GOARCH=arm64 GOOS=darwin go build -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY_NAME}-arm64" .

    info "Building amd64 (CGo cross-compile via clang -arch x86_64)..."
    CGO_ENABLED=1 \
    CGO_CFLAGS="-arch x86_64" \
    CGO_LDFLAGS="-arch x86_64" \
    CC="clang -arch x86_64" \
    GOARCH=amd64 GOOS=darwin \
    go build -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY_NAME}-amd64" .

    lipo -create -output "${BUILD_DIR}/${BINARY_NAME}" \
        "${BUILD_DIR}/${BINARY_NAME}-arm64" \
        "${BUILD_DIR}/${BINARY_NAME}-amd64"
    rm "${BUILD_DIR}/${BINARY_NAME}-arm64" "${BUILD_DIR}/${BINARY_NAME}-amd64"
    ok "Universal binary: ${BUILD_DIR}/${BINARY_NAME}"
else
    GOARCH="$GOARCH" GOOS=darwin go build -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY_NAME}" .
    ok "Binary (${GOARCH}): ${BUILD_DIR}/${BINARY_NAME}"
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
    <key>CFBundleSignature</key>       <string>????</string>
    <key>LSMinimumSystemVersion</key>  <string>11.0</string>
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

# Add a README
cat > "${DMG_STAGING}/README.txt" << README
My Monitor v${VERSION}
──────────────────────
1. Drag MyMonitor.app to the Applications folder.
2. Double-click MyMonitor in Applications to start.
3. Your browser will open http://localhost:${PORT} automatically.
4. Default login: admin / admin
   ⚠ Change your password in the Setup wizard!

To uninstall: delete MyMonitor.app from Applications.
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
