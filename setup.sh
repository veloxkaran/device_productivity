#!/bin/bash
set -e

echo "==> Checking for Homebrew..."
if ! command -v brew &>/dev/null; then
  echo "Installing Homebrew..."
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
fi

echo "==> Checking for Go..."
if ! command -v go &>/dev/null; then
  echo "Installing Go via Homebrew..."
  brew install go
fi

echo "==> Go version: $(go version)"

echo "==> Downloading dependencies..."
go mod tidy

echo "==> Building binary (Mac Intel amd64)..."
GOARCH=amd64 GOOS=darwin go build -o my-monitor .

echo ""
echo "======================================"
echo "  Build complete! Run with:"
echo "  ./my-monitor"
echo ""
echo "  Then open: http://localhost:8090"
echo "  Default login: admin / admin"
echo "======================================"
