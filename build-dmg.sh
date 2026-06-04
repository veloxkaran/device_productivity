#!/bin/bash
set -e

APP_NAME="MyMonitor"
BINARY_NAME="my-monitor"
VERSION="1.0.0"
BUNDLE_ID="com.mymonitor.app"
DMG_NAME="${APP_NAME}-${VERSION}.dmg"
BUILD_DIR="./dist"
APP_BUNDLE="${BUILD_DIR}/${APP_NAME}.app"

echo "==> Cleaning build dir"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

echo "==> Building universal binary (arm64 + amd64)"
GOOS=darwin GOARCH=arm64 go build -o "${BUILD_DIR}/${BINARY_NAME}-arm64" .
GOOS=darwin GOARCH=amd64 go build -o "${BUILD_DIR}/${BINARY_NAME}-amd64" .
lipo -create -output "${BUILD_DIR}/${BINARY_NAME}" \
    "${BUILD_DIR}/${BINARY_NAME}-arm64" \
    "${BUILD_DIR}/${BINARY_NAME}-amd64"
rm "${BUILD_DIR}/${BINARY_NAME}-arm64" "${BUILD_DIR}/${BINARY_NAME}-amd64"

echo "==> Creating .app bundle"
mkdir -p "${APP_BUNDLE}/Contents/MacOS"
mkdir -p "${APP_BUNDLE}/Contents/Resources"

# Copy binary into bundle
cp "${BUILD_DIR}/${BINARY_NAME}" "${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}"
chmod +x "${APP_BUNDLE}/Contents/MacOS/${BINARY_NAME}"

# Launcher script: starts the server, waits for it, then opens browser
cat > "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}" << 'LAUNCHER'
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
DATA_DIR="$HOME/Library/Application Support/MyMonitor"
mkdir -p "$DATA_DIR"
cd "$DATA_DIR"
exec "$DIR/my-monitor"
LAUNCHER
chmod +x "${APP_BUNDLE}/Contents/MacOS/${APP_NAME}"

# Info.plist
cat > "${APP_BUNDLE}/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>My Monitor</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleSignature</key>
    <string>????</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
PLIST

echo "==> Creating DMG with hdiutil"
DMG_STAGING="${BUILD_DIR}/dmg-staging"
mkdir -p "$DMG_STAGING"
cp -r "${APP_BUNDLE}" "$DMG_STAGING/"

# Create a symlink to /Applications for drag-and-drop install
ln -s /Applications "${DMG_STAGING}/Applications"

TEMP_DMG="${BUILD_DIR}/temp.dmg"
hdiutil create \
    -volname "${APP_NAME}" \
    -srcfolder "$DMG_STAGING" \
    -ov \
    -format UDRW \
    "$TEMP_DMG"

# Convert to compressed read-only DMG
hdiutil convert "$TEMP_DMG" \
    -format UDZO \
    -imagekey zlib-level=9 \
    -o "${BUILD_DIR}/${DMG_NAME}"

rm -f "$TEMP_DMG"
rm -rf "$DMG_STAGING"

echo ""
echo "  ┌──────────────────────────────────────────────────────┐"
echo "  │  Build complete!                                     │"
echo "  │  DMG: dist/${DMG_NAME}          │"
echo "  │                                                      │"
echo "  │  Install: open the DMG and drag MyMonitor.app        │"
echo "  │           to your Applications folder.              │"
echo "  │  Launch: open MyMonitor.app — then visit            │"
echo "  │          http://localhost:8080 in your browser       │"
echo "  └──────────────────────────────────────────────────────┘"
echo ""
