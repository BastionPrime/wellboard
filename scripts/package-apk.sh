#!/bin/sh
# Build a real .apk package with apk-tools 3 (`apk mkpkg`), the same
# tool the OpenWrt 25.12 buildroot uses. No hand-rolled tar: the
# package container (v3) and its metadata are produced by apk itself.
#
# OpenWrt 25.12 installs these with `apk add --allow-untrusted` (they
# are unsigned, like every previous WellBoard release).
#
# Usage: package-apk.sh <name> <version> <release> <arch> <src>
#   src = prebuilt binary path (wellboard only; ignored for luci-app)
#
# Toolchain resolution (first match wins):
#   1. $APK_BIN              — explicit apk(8) with the mkpkg applet
#   2. apk on PATH           — a host/OpenWrt/Alpine apk-tools 3
#   3. docker $APK_IMAGE     — default alpine:latest (apk-tools 3)
# Packages are UNSIGNED (apk installs them with --allow-untrusted); this
# matches how the previous releases were produced and is documented in
# docs/INSTALL.md.

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
APK_IMAGE="${APK_IMAGE:-alpine:latest}"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$STAGE" "$DIST"

# Stage the rootfs tree exactly like the OpenWrt package Makefile
# install step (packaging/openwrt/Makefile, Package/*/install).
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
    DESC="Remnawave subscription manager for nikki/mihomo (web UI on :8090)"
    ;;
luci-app-wellboard)
    cp -r "$PKG/luci-app-wellboard/root/." "$STAGE/"
    mkdir -p "$STAGE/www"
    cp -r "$PKG/luci-app-wellboard/htdocs/." "$STAGE/www/"
    DEPENDS="luci-base wellboard"
    DESC="LuCI menu entry for the WellBoard web UI"
    ;;
*)
    echo "unknown package: $NAME" >&2
    exit 1
    ;;
esac

OUT="${NAME}-${VERSION}-r${RELEASE}-${ARCH}.apk"

# meta_key:value pairs (apk-tools 3 package metadata). Values with
# spaces are single-quoted because this string is handed to `sh -c`.
MKPKG_ARGS="-F /w/data -o /dist/$OUT \
    -I name:$NAME \
    -I version:${VERSION}-r${RELEASE} \
    -I description:'$DESC' \
    -I url:https://github.com/BastionPrime/wellboard \
    -I license:AGPL-3.0-only \
    -I arch:$ARCH \
    -I depends:'$DEPENDS'"

if [ -n "${APK_BIN:-}" ]; then
    "$APK_BIN" mkpkg -F "$STAGE" -o "$DIST/$OUT" \
        -I name:"$NAME" -I version:"${VERSION}-r${RELEASE}" \
        -I description:"$DESC" \
        -I url:https://github.com/BastionPrime/wellboard \
        -I license:AGPL-3.0-only -I arch:"$ARCH" -I depends:"$DEPENDS"
elif command -v apk >/dev/null 2>&1; then
    apk mkpkg -F "$STAGE" -o "$DIST/$OUT" \
        -I name:"$NAME" -I version:"${VERSION}-r${RELEASE}" \
        -I description:"$DESC" \
        -I url:https://github.com/BastionPrime/wellboard \
        -I license:AGPL-3.0-only -I arch:"$ARCH" -I depends:"$DEPENDS"
else
    # shellcheck disable=SC2086
    docker run --rm -v "$WORK":/w -v "$DIST":/dist "$APK_IMAGE" \
        sh -c "apk mkpkg $MKPKG_ARGS"
    # docker runs as root; hand the artifact back to the caller.
    if [ "$(id -u)" != "0" ]; then
        chown "$(id -u):$(id -g)" "$DIST/$OUT" 2>/dev/null || true
    fi
fi

echo "built: $DIST/$OUT"