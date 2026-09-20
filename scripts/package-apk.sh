#!/bin/sh
# Build an .apk package (apk-tools v2 static tarball layout) for
# wellboard / luci-app-wellboard.
#
# OpenWrt 24.10+ moved to apk. An "apk v2" package is a gzip'd tar with
# leading .PKGINFO / .sign... meta entries plus the rootfs tree. The
# signature section is omitted (unsigned local package; apk requires
# --allow-untrusted for such packages — documented in INSTALL.md).
#
# Usage: package-apk.sh <name> <version> <release> <arch> <src>
#   src = prebuilt binary path (wellboard only; ignored for luci-app)

set -eu

NAME=$1
VERSION=$2
RELEASE=$3
ARCH=$4
SRC=$5

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG="$ROOT/packaging/openwrt"
WORK="$(mktemp -d)"
STAGE="$WORK/data"
DIST="$ROOT/dist"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$STAGE" "$DIST"

# Stage the rootfs tree exactly like scripts/package-ipk.sh.
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
    DEPENDS="ca-bundle curl nikki"
    ;;
luci-app-wellboard)
    cp -r "$PKG/luci-app-wellboard/root/." "$STAGE/"
    mkdir -p "$STAGE/www"
    cp -r "$PKG/luci-app-wellboard/htdocs/." "$STAGE/www/"
    DEPENDS="luci-base wellboard"
    ;;
*)
    echo "unknown package: $NAME" >&2
    exit 1
    ;;
esac

# .PKGINFO (apk v2 metadata)
{
    echo "pkgname = $NAME"
    echo "pkgver = $VERSION-r$RELEASE"
    echo "arch = $ARCH"
    echo "pkgdesc = Remnawave subscription manager for nikki/mihomo (web UI on :8090)"
    echo "url = https://wellboard.local"
    echo "builddate = $(date +%s)"
    echo "packager = WellBoard project <dev@wellboard.local>"
    echo "size = $(du -sk "$STAGE" | cut -f1)000"
    echo "depend = $DEPENDS"
} > "$STAGE/.PKGINFO"

OUT="$DIST/${NAME}-${VERSION}-r${RELEASE}-${ARCH}.apk"
( cd "$STAGE" && tar czf "$OUT" . )

echo "built: $OUT"
