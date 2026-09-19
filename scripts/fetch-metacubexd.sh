#!/bin/sh
# fetch-metacubexd.sh — download the MetaCubeXD (metacubexd) release
# dist for the monitoring tab (initial TZ 5.9 / FR-8, Phase 5).
#
# Usage: scripts/fetch-metacubexd.sh [<version>]   (default: latest)
# Installs into ui/metacubexd/ (gitignored; ~8 MB unpacked — too large
# to vendor per DECISIONS D18). The dry-run/dev server serves the SPA
# from this directory when present; without it the Monitoring tab
# shows an honest "fetch the dist" notice.
#
# Release asset: compressed-dist.tgz (verified 2026-09-20, v1.273.1).

set -eu

REPO="MetaCubeX/metacubexd"
DEST_DIR="ui/metacubexd"
VERSION="${1:-}"

log() { printf 'fetch-metacubexd: %s\n' "$*"; }

command -v curl >/dev/null 2>&1 || { log "curl is required"; exit 1; }
command -v tar >/dev/null 2>&1 || { log "tar is required"; exit 1; }

if [ -z "$VERSION" ]; then
  log "resolving latest stable release of ${REPO}"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  if [ -z "$VERSION" ]; then
    log "could not resolve latest release; pass a version explicitly (e.g. v1.273.1)"
    exit 1
  fi
fi
log "version: ${VERSION}"

URL="https://github.com/${REPO}/releases/download/${VERSION}/compressed-dist.tgz"
log "downloading ${URL}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
curl -fsSL -o "${TMP_DIR}/dist.tgz" "$URL"

rm -rf "$DEST_DIR"
mkdir -p "$DEST_DIR"
tar xzf "${TMP_DIR}/dist.tgz" -C "$DEST_DIR"

if [ ! -f "${DEST_DIR}/index.html" ]; then
  log "archive did not contain index.html — unexpected layout"
  exit 1
fi

echo "${VERSION}" > "${DEST_DIR}/VERSION"
log "ok: ${DEST_DIR} is ready ($(find "$DEST_DIR" -type f | wc -l) files)"
