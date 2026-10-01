#!/usr/bin/env bash
# Build Windows+Linux agents and publish with VERSIONED FILENAMES so:
#   - they appear in the dashboard Downloads tab (hub parses version from name),
#   - each release is a fresh URL (Cloudflare can't serve a stale copy),
#   - the hub's single-segment /downloads/{name} route serves them.
# Run on the hub server:  cd /opt/hajir/hajir-monitor && ./release-server.sh
set -euo pipefail
cd "$(dirname "$0")"

HUB_URL="https://monitor.veloxlabs.net"
API_URL="https://dev.veloxlabs.net/api/v2"
V="$(sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' cloud/heartbeat.go)"
: "${V:?could not read AppVersion from cloud/heartbeat.go}"
echo "==> Releasing version $V"

echo "==> 1/5 Ensure hub uses the real backend API (avoids self-call 404)"
sed -i "s#^HAJIR_API_URL=.*#HAJIR_API_URL=${API_URL}#" .env.production
grep HAJIR_API_URL .env.production

echo "==> 2/5 Build Windows + Linux in Docker (macOS must be built on a Mac)"
docker run --rm -v "$PWD":/src -w /src \
  -e HAJIR_API_URL="$API_URL" -e HUB_URL="$HUB_URL" -e GOFLAGS=-buildvcs=false \
  golang:1.22-bookworm bash -c '
    set -e
    git config --global --add safe.directory /src 2>/dev/null || true
    make build-windows-amd64 build-windows-arm64 build-linux-amd64 build-linux-arm64
    make package-windows-amd64 package-windows-arm64 package-linux-amd64 package-linux-arm64
  '

echo "==> 3/5 Publish versioned filenames into ./downloads"
cd downloads
# people-facing installers (shown in the Downloads tab)
cp -f ../dist/MyMonitor-Setup-windows-amd64.exe "MyMonitor-Setup-windows-amd64-v${V}.exe"
cp -f ../dist/MyMonitor-Setup-windows-arm64.exe "MyMonitor-Setup-windows-arm64-v${V}.exe"
cp -f ../dist/MyMonitor-Setup-linux-amd64       "MyMonitor-Setup-linux-amd64-v${V}"
cp -f ../dist/MyMonitor-Setup-linux-arm64       "MyMonitor-Setup-linux-arm64-v${V}"
# raw auto-update binaries (hidden from the tab; referenced by latest.json)
cp -f ../dist/my-monitor-windows-amd64.exe "my-monitor-windows-amd64-v${V}.exe"
cp -f ../dist/my-monitor-windows-arm64.exe "my-monitor-windows-arm64-v${V}.exe"
cp -f ../dist/my-monitor-linux-amd64       "my-monitor-linux-amd64-v${V}"
cp -f ../dist/my-monitor-linux-arm64       "my-monitor-linux-arm64-v${V}"

# remove stale UNVERSIONED installers so the tab shows only fresh versioned ones
rm -f MyMonitor-Setup-windows-amd64.exe MyMonitor-Setup-windows-arm64.exe \
      MyMonitor-Setup-linux-amd64 MyMonitor-Setup-linux-arm64 \
      my-monitor-windows-amd64.exe my-monitor-windows-arm64.exe \
      my-monitor-linux-amd64 my-monitor-linux-arm64 2>/dev/null || true
rm -rf ./[0-9]*.[0-9]* 2>/dev/null || true   # drop any old versioned SUBfolders

# ── Legacy-OS build (opt-in) ──────────────────────────────────────────────────
# BUILD_LEGACY=1 ./release-server.sh  — adds Windows 7/8 (64 & 32-bit) and old-Linux
# installers, built with Go 1.20 on the "-legacy" update lane so old machines stay on
# legacy binaries. macOS legacy must be built on a Mac (MIN_MACOS=10.13 bash build-dmg.sh).
if [ "${BUILD_LEGACY:-0}" = "1" ]; then
  echo "==> 3b/5 Build LEGACY (Go 1.20): Win7/8 64&32-bit + old Linux"
  cd ..
  docker run --rm -v "$PWD":/src -w /src \
    -e HAJIR_API_URL="$API_URL" -e HUB_URL="$HUB_URL" -e GOFLAGS=-buildvcs=false \
    -e GOTOOLCHAIN=local \
    golang:1.20-bullseye bash -c '
      set -e
      git config --global --add safe.directory /src 2>/dev/null || true
      # Go 1.20 rejects the committed "go 1.22" directive; pin it to 1.20 for this build
      # only (the min/max shim covers the builtins), and always restore it afterward.
      cp go.mod go.mod.legacybak
      trap "mv -f go.mod.legacybak go.mod" EXIT
      sed -i "s/^go 1\.22$/go 1.20/" go.mod
      make SKIP_TIDY=1 GO_TAGS=legacyos \
           build-windows-amd64 build-windows-386 build-linux-amd64 build-linux-arm64 \
           package-windows-amd64 package-windows-386 package-linux-amd64 package-linux-arm64
    '
  cd downloads
  # versioned legacy installers (shown in the Downloads tab, labelled "legacy OS")
  cp -f ../dist/MyMonitor-Setup-windows-amd64.exe "MyMonitor-Setup-windows-amd64-legacy-v${V}.exe"
  cp -f ../dist/MyMonitor-Setup-windows-386.exe   "MyMonitor-Setup-windows-386-legacy-v${V}.exe"
  cp -f ../dist/MyMonitor-Setup-linux-amd64       "MyMonitor-Setup-linux-amd64-legacy-v${V}"
  cp -f ../dist/MyMonitor-Setup-linux-arm64       "MyMonitor-Setup-linux-arm64-legacy-v${V}"
  # versioned legacy raw auto-update binaries (hidden; referenced by latest.json)
  cp -f ../dist/my-monitor-windows-amd64.exe "my-monitor-windows-amd64-legacy-v${V}.exe"
  cp -f ../dist/my-monitor-windows-386.exe   "my-monitor-windows-386-legacy-v${V}.exe"
  cp -f ../dist/my-monitor-linux-amd64       "my-monitor-linux-amd64-legacy-v${V}"
  cp -f ../dist/my-monitor-linux-arm64       "my-monitor-linux-arm64-legacy-v${V}"
  echo "   Legacy artifacts published (…-legacy-v${V}…)."
else
  echo "==> 3b/5 Skipping legacy build (set BUILD_LEGACY=1 for Win7/8 + old-OS installers)"
fi

echo "==> 4/5 Write latest.json (points at versioned raw binaries)"
{
  echo '{'; echo "  \"version\": \"$V\","; echo "  \"notes\": \"Auto-update to $V\","; echo '  "assets": {'
  first=1
  for kv in "linux/amd64:my-monitor-linux-amd64-v${V}" "linux/arm64:my-monitor-linux-arm64-v${V}" \
            "windows/amd64:my-monitor-windows-amd64-v${V}.exe" "windows/arm64:my-monitor-windows-arm64-v${V}.exe" \
            "linux/amd64-legacy:my-monitor-linux-amd64-legacy-v${V}" "linux/arm64-legacy:my-monitor-linux-arm64-legacy-v${V}" \
            "windows/amd64-legacy:my-monitor-windows-amd64-legacy-v${V}.exe" "windows/386-legacy:my-monitor-windows-386-legacy-v${V}.exe"; do
    key=${kv%%:*}; f=${kv#*:}; [ -f "$f" ] || continue
    sha=$(sha256sum "$f" | awk '{print $1}')
    [ $first -eq 1 ] || printf ',\n'; first=0
    printf '    "%s": {"url": "%s/downloads/%s", "sha256": "%s"}' "$key" "$HUB_URL" "$f" "$sha"
  done
  printf '\n  }\n}\n'
} > latest.json
cat latest.json
cd ..

echo "==> 5/5 Restart hub"
docker compose --env-file .env.production up -d
sleep 3
docker compose logs --since=20s hub | grep -i verify || echo "(no verify errors)"

echo; echo "== verify fresh through Cloudflare =="
curl -sI "$HUB_URL/downloads/MyMonitor-Setup-windows-amd64-v${V}.exe" | grep -iE "content-length|cf-cache-status" || true
echo; echo "DONE. Version $V"
echo "Windows x64 install URL: $HUB_URL/downloads/MyMonitor-Setup-windows-amd64-v${V}.exe"
