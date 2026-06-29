#!/bin/sh
set -eu

REPO="${WORKSPACE_CLI_REPO:-idefav/workspace-cli}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="${BIN_NAME:-workspace}"

fail() {
  echo "workspace-cli install: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

detect_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux) echo "linux" ;;
    darwin) echo "darwin" ;;
    *) fail "unsupported OS: $os" ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64 | amd64) echo "amd64" ;;
    arm64 | aarch64) echo "arm64" ;;
    *) fail "unsupported architecture: $arch" ;;
  esac
}

download() {
  url="$1"
  out="$2"
  curl -fsSL "$url" -o "$out"
}

sha256_file() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    fail "missing sha256sum or shasum"
  fi
}

install_file() {
  src="$1"
  dst="$2"
  if mkdir -p "$INSTALL_DIR" 2>/dev/null && cp "$src" "$dst" 2>/dev/null && chmod 0755 "$dst" 2>/dev/null; then
    return 0
  fi
  if command -v sudo >/dev/null 2>&1; then
    sudo mkdir -p "$INSTALL_DIR"
    sudo cp "$src" "$dst"
    sudo chmod 0755 "$dst"
    return 0
  fi
  fail "cannot write to $INSTALL_DIR; rerun with INSTALL_DIR set to a writable directory"
}

need curl
need tar
need awk
need grep

OS="$(detect_os)"
ARCH="$(detect_arch)"
LATEST_URL="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")"
VERSION="${LATEST_URL##*/}"
[ -n "$VERSION" ] && [ "$VERSION" != "latest" ] || fail "could not resolve latest release"

ASSET="workspace-cli_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "Installing workspace-cli ${VERSION} for ${OS}/${ARCH}"
download "${BASE_URL}/checksums.txt" "${TMP_DIR}/checksums.txt"
download "${BASE_URL}/${ASSET}" "${TMP_DIR}/${ASSET}"

expected="$(grep " ${ASSET}\$" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
[ -n "$expected" ] || fail "checksum not found for ${ASSET}"
actual="$(sha256_file "${TMP_DIR}/${ASSET}")"
[ "$actual" = "$expected" ] || fail "checksum mismatch for ${ASSET}"

tar -xzf "${TMP_DIR}/${ASSET}" -C "$TMP_DIR"
binary="$(find "$TMP_DIR" -type f -name workspace | head -n 1)"
[ -n "$binary" ] || fail "workspace binary not found in archive"

install_file "$binary" "${INSTALL_DIR}/${BIN_NAME}"
echo "workspace-cli installed to ${INSTALL_DIR}/${BIN_NAME}"
"${INSTALL_DIR}/${BIN_NAME}" version

