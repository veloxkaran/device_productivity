#!/usr/bin/env bash
# Build the PRODUCTION macOS installer on a Mac and publish it to the hub.
# Usage (on a Mac with Xcode CLT + Go):  ./release-mac.sh            # build + verify only
#                                         HUB_SSH=user@host ./release-mac.sh   # also upload
set -euo pipefail
cd "$(dirname "$0")"
[[ "$(uname)" == "Darwin" ]] || { echo "✗ Must run on macOS." >&2; exit 1; }

export HUB_URL="https://monitor.veloxlabs.net"
export HAJIR_API_URL="https://dev.veloxlabs.net/api/v2"
export MIN_MACOS="${MIN_MACOS:-10.15}"
HUB_DIR="${HUB_DIR:-/opt/hajir/hajir-monitor/downloads}"
V="$(sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' cloud/heartbeat.go)"
: "${V:?could not read AppVersion}"
echo "==> Building MyMonitor v$V (min macOS $MIN_MACOS) for $HUB_URL"

ARCH=universal VERSION="$V" bash build-dmg.sh

APP_BIN="dist/MyMonitor.app/Contents/MacOS/MyMonitor"
echo "==> Verifying deployment target"
for a in $(lipo -archs "$APP_BIN"); do
  m=$(vtool -arch "$a" -show-build "$APP_BIN" | awk '/minos/{print $2; exit}')
  echo "   $a minos=$m"
  major=${m%%.*}
  if [[ "$a" == x86_64 && "$major" -gt 10 ]] || [[ "$a" == arm64 && "$major" -gt 11 ]]; then
    echo "✗ $a requires macOS $m — refusing to publish." >&2; exit 1
  fi
done
lipo -archs "$APP_BIN" | grep -q x86_64 || { echo "✗ No Intel slice." >&2; exit 1; }

OUT="dist/MyMonitor-${V}.dmg"
[[ -f "$OUT" ]] || { echo "✗ $OUT not produced." >&2; exit 1; }
# Raw auto-update binaries, pinned the same way.
make build-mac-amd64 build-mac-arm64 HAJIR_API_URL="$HAJIR_API_URL" HUB_URL="$HUB_URL" MIN_MACOS="$MIN_MACOS"
echo "✓ Built $OUT"

if [[ -n "${HUB_SSH:-}" ]]; then
  echo "==> Uploading to $HUB_SSH:$HUB_DIR"
  scp "$OUT" "$HUB_SSH:$HUB_DIR/MyMonitor-${V}.dmg"
  scp dist/my-monitor-darwin-amd64 "$HUB_SSH:$HUB_DIR/my-monitor-darwin-amd64-v${V}"
  scp dist/my-monitor-darwin-arm64 "$HUB_SSH:$HUB_DIR/my-monitor-darwin-arm64-v${V}"
  # Drop older (broken, macOS-26-only) dmgs so the Downloads tab offers only this one.
  ssh "$HUB_SSH" "cd '$HUB_DIR' && ls MyMonitor-*.dmg | grep -v 'MyMonitor-${V}.dmg' | xargs -r rm -f"
  echo "✓ Published. Check: $HUB_URL and the dashboard Activity → Downloads tab."
else
  echo "ℹ  Not uploaded. Re-run with HUB_SSH=user@host to publish."
fi
