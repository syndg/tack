#!/bin/sh
set -eu

REPO="${TACK_REPO:-syndg/tack}"
VERSION="${TACK_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

usage() {
  printf '%s\n' "Install Tack from GitHub Releases."
  printf '%s\n' ""
  printf '%s\n' "Environment variables:"
  printf '%s\n' "  TACK_VERSION=v0.1.0-alpha.1   Install a specific version. Default: latest"
  printf '%s\n' "  INSTALL_DIR=~/bin              Install directory. Default: /usr/local/bin"
  printf '%s\n' "  TACK_REPO=owner/repo           GitHub repo. Default: syndg/tack"
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
  exit 0
fi

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '%s\n' "error: required command not found: $1" >&2
    exit 1
  fi
}

need curl
need tar
need uname
need mktemp

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Darwin) os="Darwin" ;;
  Linux) os="Linux" ;;
  *)
    printf '%s\n' "error: unsupported OS: $os" >&2
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64) arch="x86_64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    printf '%s\n' "error: unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

if [ "$VERSION" = "latest" ]; then
  url="https://github.com/$REPO/releases/latest/download/tack_${os}_${arch}.tar.gz"
else
  url="https://github.com/$REPO/releases/download/$VERSION/tack_${os}_${arch}.tar.gz"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

printf '%s\n' "Downloading $url"
curl -fsSL "$url" -o "$tmp/tack.tar.gz"
tar -xzf "$tmp/tack.tar.gz" -C "$tmp"

if [ ! -f "$tmp/tack" ]; then
  printf '%s\n' "error: archive did not contain tack binary" >&2
  exit 1
fi

if [ -d "$INSTALL_DIR" ]; then
  :
elif mkdir -p "$INSTALL_DIR" 2>/dev/null; then
  :
else
  need sudo
  sudo mkdir -p "$INSTALL_DIR"
fi

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmp/tack" "$INSTALL_DIR/tack"
  chmod +x "$INSTALL_DIR/tack"
else
  need sudo
  sudo mv "$tmp/tack" "$INSTALL_DIR/tack"
  sudo chmod +x "$INSTALL_DIR/tack"
fi

printf '%s\n' "Installed tack to $INSTALL_DIR/tack"
"$INSTALL_DIR/tack" version
