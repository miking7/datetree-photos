#!/bin/sh
set -eu

REPO="miking7/datetree-photos"
INSTALL_DIR="$HOME/.local/bin"
BIN_NAME="datetree"

case "$(uname -s)" in
    Darwin) OS_NAME=darwin ;;
    Linux)  OS_NAME=linux  ;;
    *)      echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
    arm64|aarch64) ARCH_NAME=arm64 ;;
    x86_64|amd64)  ARCH_NAME=amd64 ;;
    *)             echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

echo "==> Detected ${OS_NAME}-${ARCH_NAME}"

echo "==> Resolving latest release"
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$TAG" ]; then echo "could not resolve latest release tag" >&2; exit 1; fi
echo "==> Latest release: $TAG"

ARCHIVE="datetree_${OS_NAME}_${ARCH_NAME}.tar.gz"
URL_BASE="https://github.com/${REPO}/releases/download/${TAG}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

echo "==> Downloading $ARCHIVE"
curl -fsSL -o "$TMP/$ARCHIVE" "$URL_BASE/$ARCHIVE"
curl -fsSL -o "$TMP/checksums.txt" "$URL_BASE/checksums.txt"

echo "==> Verifying SHA256"
EXPECTED=$(awk -v a="$ARCHIVE" '$2 == a { print $1 }' "$TMP/checksums.txt")
if [ -z "$EXPECTED" ]; then echo "no checksum entry for $ARCHIVE" >&2; exit 1; fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
else
    echo "no sha256 tool found (sha256sum or shasum)" >&2
    exit 1
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then echo "checksum mismatch for $ARCHIVE" >&2; exit 1; fi

echo "==> Extracting"
tar -C "$TMP" -xzf "$TMP/$ARCHIVE"

echo "==> Installing to ${INSTALL_DIR}/${BIN_NAME}"
mkdir -p "$INSTALL_DIR"
mv "$TMP/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
chmod +x "$INSTALL_DIR/$BIN_NAME"

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) echo "==> NOTE: $INSTALL_DIR is not in your PATH. Add to ~/.zshrc or ~/.bashrc:"
       echo "       export PATH=\"\$HOME/.local/bin:\$PATH\""
       ;;
esac

echo "==> Done. Run: datetree"
