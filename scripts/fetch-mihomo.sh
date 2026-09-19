#!/bin/sh
# fetch-mihomo.sh — download a stable mihomo release for dev-mode
# validation (initial TZ 7, Phase 0/1: "script to obtain the mihomo
# binary for dev mode"; docs/DECISIONS.md D3 has no Go toolchain on the
# host, and mihomo -t needs the real binary).
#
# Usage: scripts/fetch-mihomo.sh [<version>]   (default: latest stable)
# Installs to bin/mihomo (gitignored) and verifies it runs (mihomo -v).

set -eu

REPO="MetaCubeX/mihomo"
DEST_DIR="bin"
DEST="${DEST_DIR}/mihomo"
VERSION="${1:-}"

log() { printf 'fetch-mihomo: %s\n' "$*"; }

command -v curl >/dev/null 2>&1 || { log "curl is required"; exit 1; }
command -v tar >/dev/null 2>&1 || { log "tar is required"; exit 1; }

# Resolve the version: explicit arg, else the latest non-prerelease tag
# from the GitHub API.
if [ -z "$VERSION" ]; then
  log "resolving latest stable release of ${REPO}"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  if [ -z "$VERSION" ]; then
    log "could not resolve latest release; pass a version explicitly"
    exit 1
  fi
fi
log "version: ${VERSION}"

# Detect the host platform (dev machines are linux/amd64 by default).
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7|armv7l) ARCH="armv7" ;;
  *) log "unsupported arch: ${ARCH}"; exit 1 ;;
esac

case "$OS" in
  linux) ;;
  *) log "unsupported os: ${OS} (dev script targets linux)"; exit 1 ;;
esac

# mihomo release assets embed the version with a "v" prefix:
# mihomo-linux-amd64-v1.19.31.gz (tag) vs release tag v1.19.31.
ASSET="mihomo-${OS}-${ARCH}-v${VERSION#v}.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
log "downloading ${URL}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
curl -fsSL -o "${TMP_DIR}/mihomo.gz" "$URL"

mkdir -p "$DEST_DIR"
gunzip -c "${TMP_DIR}/mihomo.gz" > "${DEST}.tmp"
chmod +x "${DEST}.tmp"
mv "${DEST}.tmp" "$DEST"

# Verify the binary actually runs on this host before declaring success.
log "verifying: ${DEST} -v"
"$DEST" -v
log "ok: ${DEST} is ready"
