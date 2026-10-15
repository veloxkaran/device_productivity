#!/usr/bin/env bash
# Code-sign a Windows .exe so Windows SmartScreen stops blocking it.
# No-op (exit 0) when no certificate is configured, so builds still work.
#
# Configure via environment (put these in .env.production, NOT in git):
#   WIN_SIGN_PFX   path to your Authenticode code-signing cert (.pfx/.p12)
#   WIN_SIGN_PASS  the cert's password
#   WIN_SIGN_TS    timestamp URL (default: DigiCert). Timestamping keeps the
#                  signature valid after the cert expires.
#
# Uses osslsigncode (cross-platform; works in the Linux build container).
#   apt-get install -y osslsigncode   # Debian/Ubuntu
#   brew install osslsigncode         # macOS
#
# Usage:  ./sign-windows.sh dist/MyMonitor-Setup-windows-amd64-v2.1.3.exe
set -euo pipefail

f="${1:?usage: sign-windows.sh <file.exe>}"
TS="${WIN_SIGN_TS:-http://timestamp.digicert.com}"

if [ -z "${WIN_SIGN_PFX:-}" ] || [ ! -f "${WIN_SIGN_PFX:-}" ]; then
  echo "    [sign] skipped (no WIN_SIGN_PFX) — $f will be UNSIGNED and SmartScreen-flagged" >&2
  exit 0
fi
if ! command -v osslsigncode >/dev/null 2>&1; then
  echo "    [sign] osslsigncode not installed — cannot sign $f" >&2
  exit 0
fi

tmp="${f}.signed"
osslsigncode sign \
  -pkcs12 "$WIN_SIGN_PFX" \
  -pass "${WIN_SIGN_PASS:-}" \
  -n "Hajir Monitor" \
  -i "https://veloxlabs.net" \
  -t "$TS" \
  -h sha256 \
  -in "$f" -out "$tmp"
mv -f "$tmp" "$f"
echo "    [sign] signed: $f"
