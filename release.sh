#!/usr/bin/env bash
# One-shot release: build installers + raw binaries -> publish to hub-data/downloads
# (incl. latest.json for auto-update) -> restart hub.
#
#   HAJIR_API_URL=https://api.example.com/api/v2 \
#   HUB_URL=https://hub.example.com \
#   ./release.sh
set -euo pipefail
cd "$(dirname "$0")"

# ── Environment selection ───────────────────────────────────────────
# Usage:  ./release.sh [staging|production]
# If omitted, it is derived from the current git branch:
#   main / master / prod*  -> production      (everything else -> staging)
ENV="${1:-}"
if [ -z "$ENV" ]; then
  BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo staging)"
  case "$BRANCH" in
    main|master|prod|production) ENV="production" ;;
    *) ENV="staging" ;;
  esac
fi
ENV_FILE=".env.$ENV"
[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE not found. Create it (see .env.staging)."; exit 1; }
echo "==> Environment: $ENV  (from $ENV_FILE)"
set -a; . "./$ENV_FILE"; set +a   # load + export all vars from the env file

: "${HAJIR_API_URL:?set HAJIR_API_URL in $ENV_FILE}"
: "${HUB_URL:?set HUB_URL in $ENV_FILE}"
export HAJIR_API_URL HUB_URL
DEST="hub-data/downloads"
mkdir -p "$DEST"

VERSION="$(sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' cloud/heartbeat.go)"
echo "==> Releasing version ${VERSION:-?}"
echo "    HAJIR_API_URL=$HAJIR_API_URL"
echo "    HUB_URL=$HUB_URL"

echo "==> 1/4  Building installers + raw binaries (Docker)"
docker run --rm -v "$PWD":/src -w /src \
  -e HAJIR_API_URL="$HAJIR_API_URL" -e HUB_URL="$HUB_URL" -e GOFLAGS=-buildvcs=false \
  golang:1.22-bookworm bash -c '
    set -e
    git config --global --add safe.directory /src 2>/dev/null || true
    make build-all
    GOOS=windows GOARCH=arm64 go build -ldflags="-s -w -H windowsgui -X my-monitor/web.DefaultHajirAPIURL=$HAJIR_API_URL -X my-monitor/web.DefaultHubURL=$HUB_URL" -o dist/my-monitor-windows-arm64.exe .
    make package-linux-amd64 package-linux-arm64 package-windows-amd64 package-windows-arm64
  '

if [ "$(uname -s)" = "Darwin" ]; then
  echo "==> 1b/4  Building macOS .dmg (native)"
  ARCH=universal bash build-dmg.sh
else
  echo "==> 1b/4  Skipping macOS .dmg (not on a Mac)"
fi

echo "==> 2/4  Publishing installers + raw binaries to $DEST"
cp -f dist/MyMonitor-Setup-* "$DEST"/ 2>/dev/null || true
cp -f dist/*.dmg "$DEST"/ 2>/dev/null || true
cp -f dist/my-monitor-darwin-amd64 dist/my-monitor-darwin-arm64 \
      dist/my-monitor-linux-amd64  dist/my-monitor-linux-arm64 \
      dist/my-monitor-windows-amd64.exe dist/my-monitor-windows-arm64.exe "$DEST"/ 2>/dev/null || true

echo "==> 3/4  Writing latest.json (auto-update manifest)"
sha() { if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }
asset() { # key file
  local key="$1" file="$DEST/$2"
  [ -f "$file" ] || return 0
  printf '    "%s": {"url": "%s/downloads/%s", "sha256": "%s"},\n' "$key" "$HUB_URL" "$2" "$(sha "$file")"
}
{
  echo '{'
  printf '  "version": "%s",\n' "$VERSION"
  printf '  "notes": "Auto-update to %s",\n' "$VERSION"
  echo '  "assets": {'
  {
    asset "darwin/amd64"  "my-monitor-darwin-amd64"
    asset "darwin/arm64"  "my-monitor-darwin-arm64"
    asset "linux/amd64"   "my-monitor-linux-amd64"
    asset "linux/arm64"   "my-monitor-linux-arm64"
    asset "windows/amd64" "my-monitor-windows-amd64.exe"
    asset "windows/arm64" "my-monitor-windows-arm64.exe"
  } | sed '$ s/,$//'   # drop trailing comma on last asset
  echo '  }'
  echo '}'
} > "$DEST/latest.json"
cat "$DEST/latest.json"

echo "==> 4/4  Restarting hub"
docker compose --env-file "$ENV_FILE" up -d --build

echo ""
echo "Done. Version $VERSION published."
echo "Managed (automatic) devices will self-update within ~6h (or on next restart)."
