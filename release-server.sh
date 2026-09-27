#!/usr/bin/env bash
# Build Windows+Linux agents, publish under a versioned folder (cache-proof),
# regenerate latest.json, and restart the hub. Run on the hub server.
#   cd /opt/hajir/hajir-monitor && ./release-server.sh
set -euo pipefail
cd "$(dirname "$0")"

HUB_URL="https://monitor.veloxlabs.net"
API_URL="https://dev.veloxlabs.net/api/v2"
VERSION="$(sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' cloud/heartbeat.go)"
: "${VERSION:?could not read AppVersion from cloud/heartbeat.go}"
echo "==> Releasing version $VERSION"

echo "==> 1/5 Ensure hub uses the real backend API (avoids the self-call 404)"
sed -i "s#^HAJIR_API_URL=.*#HAJIR_API_URL=${API_URL}#" .env.production
grep HAJIR_API_URL .env.production

echo "==> 2/5 Build Windows + Linux in Docker (skip macOS: needs cgo/mac SDK)"
docker run --rm -v "$PWD":/src -w /src \
  -e HAJIR_API_URL="$API_URL" -e HUB_URL="$HUB_URL" -e GOFLAGS=-buildvcs=false \
  golang:1.22-bookworm bash -c '
    set -e
    git config --global --add safe.directory /src 2>/dev/null || true
    make build-windows-amd64 build-windows-arm64 build-linux-amd64 build-linux-arm64
    make package-windows-amd64 package-windows-arm64 package-linux-amd64 package-linux-arm64
    ls -lh dist/
  '

echo "==> 3/5 Publish to ./downloads/$VERSION (fresh URL => Cloudflare can't serve a stale copy)"
DEST="downloads/$VERSION"
mkdir -p "$DEST"
cp -f dist/MyMonitor-Setup-windows-amd64.exe dist/MyMonitor-Setup-windows-arm64.exe \
      dist/MyMonitor-Setup-linux-amd64 dist/MyMonitor-Setup-linux-arm64 \
      dist/my-monitor-windows-amd64.exe dist/my-monitor-windows-arm64.exe \
      dist/my-monitor-linux-amd64 dist/my-monitor-linux-arm64 "$DEST"/ 2>/dev/null || true
ls -lh "$DEST"

echo "==> 4/5 Write latest.json (points at the versioned folder)"
( cd downloads
  {
    echo '{'
    echo "  \"version\": \"$VERSION\","
    echo "  \"notes\": \"Auto-update to $VERSION\","
    echo '  "assets": {'
    first=1
    for kv in \
      "linux/amd64:my-monitor-linux-amd64" "linux/arm64:my-monitor-linux-arm64" \
      "windows/amd64:my-monitor-windows-amd64.exe" "windows/arm64:my-monitor-windows-arm64.exe"; do
      key=${kv%%:*}; f=${kv#*:}
      [ -f "$VERSION/$f" ] || continue
      sha=$(sha256sum "$VERSION/$f" | awk '{print $1}')
      [ $first -eq 1 ] || printf ',\n'; first=0
      printf '    "%s": {"url": "%s/downloads/%s/%s", "sha256": "%s"}' "$key" "$HUB_URL" "$VERSION" "$f" "$sha"
    done
    printf '\n  }\n}\n'
  } > latest.json
  cat latest.json )

echo "==> 5/5 Restart hub"
docker compose --env-file .env.production up -d
sleep 3
docker compose logs --since=20s hub | grep -i verify || echo "(no verify errors)"

echo
echo "== verify fresh through Cloudflare =="
curl -sI "$HUB_URL/downloads/$VERSION/MyMonitor-Setup-windows-amd64.exe" | grep -iE "content-length|cf-cache-status" || true
echo
echo "DONE. Version $VERSION"
echo "Install URL (Windows x64):"
echo "  $HUB_URL/downloads/$VERSION/MyMonitor-Setup-windows-amd64.exe"
