#!/bin/sh
set -eu

REPO="alansikora/codecanary"
BINARY="codecanary-setup"

cleanup() { rm -rf "$TMPDIR"; }
trap cleanup EXIT INT TERM

TMPDIR=$(mktemp -d)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | sed 's/.*"v//' | sed 's/".*//')
URL="https://github.com/$REPO/releases/download/v${TAG}/codecanary-setup_${TAG}_${OS}_${ARCH}.tar.gz"

echo "Downloading CodeCanary Setup v${TAG}..."
curl -fsSL "$URL" | tar -xz -C "$TMPDIR" "$BINARY"
chmod +x "$TMPDIR/$BINARY"
"$TMPDIR/$BINARY" "$@"
