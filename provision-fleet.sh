#!/usr/bin/env bash
# Bulk-create managed device tokens and emit per-employee silent-install
# commands + provision.json files.
#
# Auth: uses your EMPLOYER dashboard session bearer token (owner scope).
#
#   export HUB_URL="https://monitor.veloxlabs.net"
#   export COMPANY_ID="123"
#   export EMPLOYER_TOKEN="<bearer copied from your logged-in dashboard>"
#   ./provision-fleet.sh employees.csv
#
# employees.csv  (header optional):  user_id,employee_name
#   11,Ram Bahadur
#   12,Sita Sharma
#
# Output (./provisioning/):
#   provision-<user_id>.json     -> copy to the target PC as data/provision.json
#   install-commands.txt         -> ready --token install command per employee
set -euo pipefail

CSV="${1:?usage: ./provision-fleet.sh employees.csv}"
: "${HUB_URL:?set HUB_URL}"; : "${COMPANY_ID:?set COMPANY_ID}"; : "${EMPLOYER_TOKEN:?set EMPLOYER_TOKEN}"
HUB_URL="${HUB_URL%/}"
FORCE="${FORCE:-0}"
VER="$(sed -n 's/.*AppVersion = "\(.*\)".*/\1/p' cloud/heartbeat.go 2>/dev/null || echo)"
WINEXE="MyMonitor-Setup-windows-amd64${VER:+-v$VER}.exe"

OUT="provisioning"; mkdir -p "$OUT"; : > "$OUT/install-commands.txt"; chmod 700 "$OUT"

# existing devices -> user_ids already provisioned (skip unless FORCE=1)
existing="$(curl -fsS -H "Authorization: Bearer $EMPLOYER_TOKEN" \
  "$HUB_URL/api/employer/$COMPANY_ID/devices" 2>/dev/null \
  | python3 -c 'import sys,json;
try:
 d=json.load(sys.stdin); a=d.get("data",d);
 print(" ".join(str(x.get("user_id")) for x in (a if isinstance(a,list) else []) if x.get("user_id")))
except Exception: print("")' 2>/dev/null || echo "")"

created=0; skipped=0; failed=0
while IFS=, read -r uid name _; do
  uid="$(echo "${uid:-}" | tr -d '[:space:]')"
  name="$(echo "${name:-}" | sed 's/^ *//; s/ *$//')"
  [ -z "$uid" ] && continue
  case "$uid" in user_id|USER_ID|"#"*) continue;; esac          # skip header/comment
  case "$uid" in ''|*[!0-9]*) echo "  ! skip invalid user_id: '$uid'"; continue;; esac

  if [ "$FORCE" != "1" ] && echo " $existing " | grep -q " $uid "; then
    echo "  = user $uid already has a device (skip; FORCE=1 to add another)"; skipped=$((skipped+1)); continue
  fi

  resp="$(curl -fsS -X POST -H "Authorization: Bearer $EMPLOYER_TOKEN" -H "Content-Type: application/json" \
    --data "{\"user_id\":$uid,\"employee_name\":\"${name//\"/}\",\"name\":\"${name//\"/} device\"}" \
    "$HUB_URL/api/employer/$COMPANY_ID/devices" 2>/dev/null || true)"
  token="$(printf '%s' "$resp" | python3 -c 'import sys,json;
try: print(json.load(sys.stdin).get("data",{}).get("token",""))
except Exception: print("")' 2>/dev/null)"

  if [ -z "$token" ]; then
    echo "  x FAILED for user $uid ${name:+($name)} -> $resp"; failed=$((failed+1)); continue
  fi

  printf '{"token":"%s","hub_url":"%s"}\n' "$token" "$HUB_URL" > "$OUT/provision-$uid.json"
  chmod 600 "$OUT/provision-$uid.json"
  printf 'user %s\t%s\n  %s --token %s\n\n' "$uid" "${name:-}" "$WINEXE" "$token" >> "$OUT/install-commands.txt"
  echo "  + created device for user $uid ${name:+($name)}"
  created=$((created+1))
done < "$CSV"

echo
echo "Done. created=$created skipped=$skipped failed=$failed"
echo "Files in ./$OUT/:"
echo "  install-commands.txt   (one --token command per employee)"
echo "  provision-<user_id>.json  (copy to that PC as data/provision.json before first launch)"
echo
echo "NOTE: tokens are shown once. Keep ./$OUT private; delete it after provisioning."
