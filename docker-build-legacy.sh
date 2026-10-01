#!/usr/bin/env bash
# Legacy-OS desktop installers (Windows 7/8, old Linux) built with Go 1.20 in Docker.
#
# Why a separate script: Go 1.21+ dropped support for Windows 7/8/Server-2012 and for
# macOS 10.13/10.14. Binaries built with the normal (Go 1.22) pipeline simply refuse to
# launch there — which is the "upgrade to a new version" wall. Go 1.20 is the last release
# that still targets those systems, so the legacy installers are built with it here.
#
# Produces, in ./dist:
#   MyMonitor-Setup-windows-amd64.exe  (Windows 7+ 64-bit)
#   MyMonitor-Setup-windows-386.exe    (Windows 7+ 32-bit)
#   MyMonitor-Setup-linux-amd64 / -arm64  (static, runs on old glibc/musl)
#
# macOS is NOT built here (needs macOS tooling). For old Macs, on a Mac run:
#   MIN_MACOS=10.13 ARCH=universal bash build-dmg.sh   # with a Go 1.20 toolchain
#
# Usage:
#   HAJIR_API_URL=https://api.example.com/api/v2 \
#   HUB_URL=https://hub.example.com \
#   ./docker-build-legacy.sh
set -euo pipefail
cd "$(dirname "$0")"

HAJIR_API_URL="${HAJIR_API_URL:-http://localhost:8001/api/v2}"
HUB_URL="${HUB_URL:-http://localhost:4010}"

echo "==> Building LEGACY installers (Go 1.20) in Docker"
echo "    HAJIR_API_URL=$HAJIR_API_URL"
echo "    HUB_URL=$HUB_URL"

docker run --rm \
  -v "$PWD":/src -w /src \
  -e HAJIR_API_URL="$HAJIR_API_URL" \
  -e HUB_URL="$HUB_URL" \
  -e GOFLAGS=-buildvcs=false \
  golang:1.20-bullseye \
  bash -c '
    set -e
    git config --global --add safe.directory /src 2>/dev/null || true
    make GO_TAGS=legacyos \
         package-windows-amd64 package-windows-386 \
         package-linux-amd64 package-linux-arm64
  '

echo ""
echo "==> Done. Legacy installers in ./dist:"
ls -lh dist/MyMonitor-Setup-windows-amd64.exe \
       dist/MyMonitor-Setup-windows-386.exe \
       dist/MyMonitor-Setup-linux-amd64 \
       dist/MyMonitor-Setup-linux-arm64 2>/dev/null || true
echo ""
echo "    Give an old Windows 7/8 laptop the -386 (32-bit) or -amd64 (64-bit) installer."
echo "    These sit alongside the normal installers; the modern ones stay the default."
