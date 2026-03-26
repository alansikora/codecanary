#!/bin/sh
set -eu

REPO="alansikora/codecanary"
BINARY="codecanary-setup"
CANARY=false

for arg in "$@"; do
  case "$arg" in
    --canary) CANARY=true ;;
  esac
done

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
if [ -z "$TAG" ]; then
  echo "Error: could not determine latest release" >&2; exit 1
fi
case "$TAG" in *[!0-9.]*)
  echo "Error: unexpected tag format: $TAG" >&2; exit 1;; esac
if [ "$CANARY" = true ]; then
  echo "Downloading CodeCanary Setup (canary)..."
else
  echo "Downloading CodeCanary Setup v${TAG}..."
fi
URL="https://github.com/$REPO/releases/download/v${TAG}/codecanary-setup_${TAG}_${OS}_${ARCH}.tar.gz"

curl -fsSL "$URL" | tar -xz -C "$TMPDIR" "$BINARY"
chmod +x "$TMPDIR/$BINARY"
"$TMPDIR/$BINARY" "$@" < /dev/tty
