#!/usr/bin/env bash
# Build the Windows + Linux desktop installers in Docker (no local Go needed).
# macOS .dmg is NOT built here — it needs macOS tools; run ./build-dmg.sh on a Mac.
#
# Usage:
#   HAJIR_API_URL=https://api.example.com/api/v2 \
#   HUB_URL=https://hub.example.com \
#   ./docker-build-installers.sh
set -euo pipefail

HAJIR_API_URL="${HAJIR_API_URL:-http://localhost:8001/api/v2}"
HUB_URL="${HUB_URL:-http://localhost:4010}"

echo "==> Building installers in Docker"
echo "    HAJIR_API_URL=$HAJIR_API_URL"
echo "    HUB_URL=$HUB_URL"

docker run --rm \
  -v "$PWD":/src -w /src \
  -e HAJIR_API_URL="$HAJIR_API_URL" \
  -e HUB_URL="$HUB_URL" \
  -e GOFLAGS=-buildvcs=false \
  golang:1.22-bookworm \
  bash -c '
    set -e
    git config --global --add safe.directory /src 2>/dev/null || true
    make package-linux-amd64 package-linux-arm64 package-windows-amd64 package-windows-arm64
  '

echo ""
echo "==> Done. Installers in ./dist:"
ls -lh dist/MyMonitor-Setup-* 2>/dev/null || true
