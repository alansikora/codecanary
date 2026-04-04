#!/bin/sh
set -eu

REPO="alansikora/codecanary"

# Prefer /usr/local/bin if writable, fall back to ~/.local/bin.
if [ -z "${INSTALL_DIR:-}" ]; then
  if [ -w /usr/local/bin ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="$HOME/.local/bin"
  fi
fi

TAG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --canary)  TAG="canary"; shift ;;
    --version) TAG="$2"; shift 2 ;;
    *)         echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

detect_os() {
  case "$(uname -s)" in
    Linux)  echo "linux" ;;
    Darwin) echo "darwin" ;;
    *)      echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    arm64|aarch64)   echo "arm64" ;;
    *)               echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

if [ -z "$TAG" ]; then
  echo "Fetching latest release..."
  TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases" \
    | grep '"tag_name"' \
    | cut -d'"' -f4 \
    | grep '^v[0-9]*\.[0-9]*\.[0-9]*' \
    | head -n1)"
  if [ -z "$TAG" ]; then
    echo "Error: could not determine latest release" >&2; exit 1
  fi
fi
case "$TAG" in *[!a-zA-Z0-9._-]*)
  echo "Error: unexpected tag format: $TAG" >&2; exit 1;; esac

echo "Fetching release ${TAG}..."
URL="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/tags/${TAG}" \
  | grep '"browser_download_url"' \
  | grep "_${OS}_${ARCH}\.tar\.gz" \
  | cut -d'"' -f4)"

if [ -z "$URL" ]; then
  echo "Error: could not find asset for ${OS}/${ARCH} in release ${TAG}" >&2
  exit 1
fi
case "$URL" in https://github.com/*) ;; *)
  echo "Error: unexpected download URL: $URL" >&2; exit 1;; esac

ARCHIVE="${URL##*/}"
_v="${ARCHIVE%.tar.gz}"
_v="${_v#codecanary_}"
VERSION="${_v%_${OS}_${ARCH}}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${TAG}/checksums.txt"

echo "Downloading codecanary ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL -o "${TMPDIR}/${ARCHIVE}" "${URL}"
curl -fsSL -o "${TMPDIR}/checksums.txt" "${CHECKSUMS_URL}"

# Verify checksum (sha256sum on Linux, shasum on macOS).
if command -v sha256sum >/dev/null 2>&1; then
  (cd "${TMPDIR}" && grep -F "${ARCHIVE}" checksums.txt | sha256sum -c --quiet -)
elif command -v shasum >/dev/null 2>&1; then
  (cd "${TMPDIR}" && grep -F "${ARCHIVE}" checksums.txt | shasum -a 256 -c --quiet -)
else
  echo "Warning: no sha256 tool found, skipping checksum verification" >&2
fi

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}"

mkdir -p "${INSTALL_DIR}"
cp "${TMPDIR}/codecanary" "${INSTALL_DIR}/codecanary"
chmod +x "${INSTALL_DIR}/codecanary"

# macOS: ad-hoc re-sign so Gatekeeper/AppleSystemPolicy allows execution.
# Binaries cross-compiled on Linux carry a linker-signed signature that macOS
# rejects ("load code signature error 2"). Local ad-hoc signing fixes this.
if [ "$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then
  codesign --force --sign - "${INSTALL_DIR}/codecanary" 2>/dev/null || true
fi

echo ""
echo "codecanary ${VERSION} installed to ${INSTALL_DIR}/codecanary"

# Check if install dir is on PATH.
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "Add ${INSTALL_DIR} to your PATH:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

echo ""
echo "Run 'codecanary setup' to get started."
