#!/bin/sh
# Build a legacy .ipk package for wellboard / luci-app-wellboard.
#
# An .ipk is an `ar` archive containing:
#   debian-binary  ("2.0")
#   control.tar.gz (control file with Package/Version/Depends...)
#   data.tar.gz    (the ./usr/... / ./etc/... rootfs tree)
#
# The file layout mirrors what packaging/openwrt/Makefile installs
# (Package/<name>/install): same paths, same permissions. The OpenWrt
# buildroot would produce the same tree; here we emulate it so the
# package can be produced without a full buildroot checkout.
#
# Usage: package-ipk.sh <name> <version> <openwrt-arch> <src>
#   name     wellboard | luci-app-wellboard
#   version  package version (PKG_VERSION)
#   arch     OpenWrt arch (x86_64, aarch64_cortex-a53, ... or "all")
#   src      prebuilt binary (wellboard) — ignored for luci-app

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

mkdir -p "$STAGE" "$DIST"

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
    ;;
luci-app-wellboard)
    cp -r "$PKG/luci-app-wellboard/root/." "$STAGE/"
    mkdir -p "$STAGE/www"
    cp -r "$PKG/luci-app-wellboard/htdocs/." "$STAGE/www/"
    DEPENDS="luci-base, wellboard"
    ;;
*)
    echo "unknown package: $NAME" >&2
    exit 1
    ;;
esac

# control file (field set matches packaging/openwrt/Makefile)
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
    echo "Description: Remnawave subscription manager for nikki/mihomo (web UI on :8090)"
} > "$WORK/control"

( cd "$WORK" && echo 2.0 > debian-binary )
( cd "$WORK" && tar czf control.tar.gz control )
( cd "$STAGE" && tar czf "$WORK/data.tar.gz" . )

OUT="$DIST/${NAME}_${VERSION}_${ARCH}.ipk"
( cd "$WORK" && ar rc "$OUT" debian-binary control.tar.gz data.tar.gz )

echo "built: $OUT"
