#!/usr/bin/env bash
# One-step Android release: build the signed production APK, copy it to the
# server, and load it into the hub's downloads so it shows on the dashboard.
#
# Usage:
#   ./release-android.sh 1.0.2 3          # versionName versionCode
#   ./release-android.sh 1.0.2            # versionCode defaults to a timestamp
#
# Override the destination if it ever changes:
#   SERVER=root@152.53.124.158 CONTAINER=hajir-hub ./release-android.sh 1.0.3 4
set -euo pipefail

VERSION="${1:-}"
CODE="${2:-$(date +%s)}"
SERVER="${SERVER:-root@152.53.124.158}"
CONTAINER="${CONTAINER:-hajir-hub}"
DEST_DIR="${DEST_DIR:-/data/downloads}"

if [ -z "$VERSION" ]; then
  echo "usage: ./release-android.sh <versionName> [versionCode]" >&2
  echo "e.g.   ./release-android.sh 1.0.2 3" >&2
  exit 1
fi

APK_LOCAL="mobile/android/app/build/outputs/apk/release/app-release.apk"
APK_NAME="MyMonitor-android-universal-v${VERSION}.apk"
cd "$(dirname "$0")"

echo "==> Building signed production APK v${VERSION} (code ${CODE})"
make package-android-prod APP_VERSION="${VERSION}" APP_VERSION_CODE="${CODE}"

echo "==> Uploading to ${SERVER}:/tmp/${APK_NAME}"
scp "${APK_LOCAL}" "${SERVER}:/tmp/${APK_NAME}"

echo "==> Loading into ${CONTAINER}:${DEST_DIR}/"
ssh "${SERVER}" "docker cp /tmp/${APK_NAME} ${CONTAINER}:${DEST_DIR}/${APK_NAME} && rm -f /tmp/${APK_NAME}"

echo "==> Done. ${APK_NAME} is live on the dashboard's Download app tab."
echo "    Verify: ssh ${SERVER} 'curl -s http://localhost:4011/api/downloads | grep -o \"MyMonitor-android[^\\\"]*\" | sort -u'"
