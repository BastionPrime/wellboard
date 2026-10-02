#!/bin/sh
# Build a legacy .ipk with the real OpenWrt opkg toolchain
# (`ipkg-build`, the script the buildroot itself uses) instead of
# hand-rolling the ar archive.
#
# Usage: package-ipk.sh <name> <version> <arch> <src>
#   name     wellboard | luci-app-wellboard
#   version  package version (PKG_VERSION)
#   arch     OpenWrt arch (x86_64, aarch64_cortex-a53, ... or "all")
#   src      prebuilt binary (wellboard) — ignored for luci-app
#
# Toolchain resolution:
#   1. $IPKG_BUILD           — explicit path to ipkg-build
#   2. ipkg-build on PATH
#   3. pinned download of the official OpenWrt script (tag + sha256
#      checked) into the build's temp dir
# Without any of them the script FAILS: it never falls back to a
# hand-made archive (that is the whole point of this change).
#
# Where ipkg-build comes from otherwise: an OpenWrt buildroot/SDK
# checkout (scripts/ipkg-build) or the standalone opkg-utils package.
#   IPKG_BUILD=~/openwrt/scripts/ipkg-build scripts/package-ipk.sh ...
#
# It also needs GNU tar and binutils ar (the buildroot provides both).
# A busybox tar silently produces EMPTY control/data archives and a
# busybox ar cannot create the archive at all — if the resulting .ipk
# is a couple of dozen bytes, that is the toolchain, not the layout.

set -eu

NAME=$1
VERSION=$2
ARCH=$3
SRC=$4

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG="$ROOT/packaging/openwrt"
WORK="$(mktemp -d)"
STAGE="$WORK/data"
DIST="$ROOT/dist"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$STAGE/CONTROL" "$DIST"

case "$NAME" in
wellboard)
    [ -f "$SRC" ] || { echo "missing binary: $SRC" >&2; exit 1; }
    mkdir -p "$STAGE/usr/bin" "$STAGE/etc/config" "$STAGE/etc/init.d" \
             "$STAGE/etc/uci-defaults" "$STAGE/etc/wellboard" \
             "$STAGE/lib/upgrade/keep.d" \
             "$STAGE/usr/share/wellboard"
    install -m 0755 "$SRC" "$STAGE/usr/bin/wellboard"
    install -m 0644 "$PKG/files/wellboard.config" "$STAGE/etc/config/wellboard"
    install -m 0755 "$PKG/files/wellboard.init" "$STAGE/etc/init.d/wellboard"
    install -m 0755 "$PKG/files/uci-defaults.sh" "$STAGE/etc/uci-defaults/99_wellboard"
    install -m 0644 "$PKG/files/keep.d/wellboard" "$STAGE/lib/upgrade/keep.d/wellboard"
    chmod 0700 "$STAGE/etc/wellboard"
    cp -r "$ROOT/templates" "$STAGE/usr/share/wellboard/templates"
    find "$STAGE/usr/share/wellboard/templates" -type d -exec chmod 0755 {} +
    find "$STAGE/usr/share/wellboard/templates" -type f -exec chmod 0644 {} +
    DEPENDS="ca-bundle, curl, nikki"
    DESC="Remnawave subscription manager for nikki/mihomo (web UI on :8090)"
    ;;
luci-app-wellboard)
    cp -r "$PKG/luci-app-wellboard/root/." "$STAGE/"
    mkdir -p "$STAGE/www"
    cp -r "$PKG/luci-app-wellboard/htdocs/." "$STAGE/www/"
    DEPENDS="luci-base, wellboard"
    DESC="LuCI menu entry for the WellBoard web UI"
    ;;
*)
    echo "unknown package: $NAME" >&2
    exit 1
    ;;
esac

# CONTROL/control is the ipk metadata file ipkg-build reads and packs
# into control.tar.gz (it also derives the output file name from it).
{
    echo "Package: $NAME"
    echo "Version: $VERSION"
    echo "Architecture: $ARCH"
    echo "Maintainer: WellBoard project <dev@wellboard.local>"
    echo "Section: net"
    echo "Priority: optional"
    echo "Depends: $DEPENDS"
    echo "Source: wellboard"
    echo "License: AGPL-3.0-only"
    echo "Description: $DESC"
} > "$STAGE/CONTROL/control"

IPKG_BUILD="${IPKG_BUILD:-$(command -v ipkg-build 2>/dev/null || true)}"

# Pinned fallback: the official OpenWrt ipkg-build at a released tag,
# verified by sha256. Pinning (rather than tracking master) keeps the
# package layout reproducible; the checksum makes the download safe.
IPKG_BUILD_REF="v24.10.0"
IPKG_BUILD_SHA256="60714afe9b93fd0d3ecfd2fcef7839928b1a99e826ae30e2e3c0e24f1efde474"
IPKG_BUILD_URL="https://raw.githubusercontent.com/openwrt/openwrt/${IPKG_BUILD_REF}/scripts/ipkg-build"
if [ -z "$IPKG_BUILD" ] && command -v curl >/dev/null 2>&1; then
    if curl -fsSL -o "$WORK/ipkg-build" "$IPKG_BUILD_URL" 2>/dev/null &&
        echo "$IPKG_BUILD_SHA256  $WORK/ipkg-build" | sha256sum -c - >/dev/null 2>&1; then
        chmod +x "$WORK/ipkg-build"
        IPKG_BUILD="$WORK/ipkg-build"
        echo "package-ipk: using the pinned OpenWrt ipkg-build ($IPKG_BUILD_REF)"
    else
        echo "package-ipk: pinned ipkg-build download/checksum failed" >&2
    fi
fi

if [ -z "$IPKG_BUILD" ] || [ ! -x "$IPKG_BUILD" ]; then
    echo "package-ipk: ipkg-build not found." >&2
    echo "  Provide the real opkg toolchain, e.g.:" >&2
    echo "    IPKG_BUILD=<openwrt-checkout>/scripts/ipkg-build scripts/package-ipk.sh ..." >&2
    echo "  (a hand-made ar archive is deliberately NOT produced)" >&2
    exit 1
fi

# ipkg-build needs GNU tar (see the header): warn early and loudly.
if ! tar --version 2>/dev/null | head -n 1 | grep -q "GNU tar"; then
    echo "package-ipk: warning: tar is not GNU tar; ipkg-build will pack EMPTY archives." >&2
    echo "  Install GNU tar (busybox tar does not honour --format=gnu/--sort/--mtime)." >&2
fi

# ipkg-build packs $STAGE into $DIST/<Package>_<Version>_<Architecture>.ipk
# (its CLI is: ipkg-build [-v] [-h] [-m modes] <pkg_directory> [<destination>]).
"$IPKG_BUILD" "$STAGE" "$DIST" >/dev/null

echo "built: $DIST/${NAME}_${VERSION}_${ARCH}.ipk"