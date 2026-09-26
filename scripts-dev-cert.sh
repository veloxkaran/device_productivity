#!/bin/bash
set -euo pipefail
NAME="Hajir Local Signing"
if security find-identity -v -p codesigning | grep -q "$NAME"; then
  echo "✓ '$NAME' already exists — builds will use it automatically."
  exit 0
fi
TMP="$(mktemp -d)"
cat > "$TMP/cert.cnf" <<CNF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $NAME
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
CNF
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -keyout "$TMP/key.pem" -out "$TMP/cert.pem" -config "$TMP/cert.cnf" >/dev/null 2>&1
openssl pkcs12 -export -inkey "$TMP/key.pem" -in "$TMP/cert.pem" -out "$TMP/cert.p12" -passout pass:hajir -name "$NAME" -legacy 2>/dev/null \
  || openssl pkcs12 -export -inkey "$TMP/key.pem" -in "$TMP/cert.pem" -out "$TMP/cert.p12" -passout pass:hajir -name "$NAME"
security import "$TMP/cert.p12" -k "$HOME/Library/Keychains/login.keychain-db" -P hajir -T /usr/bin/codesign >/dev/null
echo "macOS will ask for your password to trust the certificate for code signing…"
security add-trusted-cert -r trustRoot -p codeSign -k "$HOME/Library/Keychains/login.keychain-db" "$TMP/cert.pem"
rm -rf "$TMP"
echo "✓ Created '$NAME'. Rebuild with: make package-macos"
