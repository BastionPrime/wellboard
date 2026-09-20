#!/bin/sh
# Cross-compile wellboard for all OpenWrt target architectures.
#
# The build host has no Go toolchain: compilation happens inside
# golang:1.23-alpine (docs/DECISIONS.md D3), CGO disabled, one binary
# per OpenWrt profile. Two aarch64 profiles share the same arm64
# binary (aarch64_cortex-a53 and aarch64_generic differ only in CPU
# tuning, not the ABI).
#
# Output: .build/wellboard-<openwrt-arch> (+ .ipk/.apk via
# scripts/package-*.sh).

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p .build

build_one() {
    _arch=$1       # OpenWrt profile name (output suffix)
    _goarch=$2     # GOARCH
    _goarm=${3:-}  # GOARM (arm only)
    _gomips=${4:-} # GOMIPS (mipsle only)

    _out=".build/wellboard-${_arch}"
    _envs="-e GOOS=linux -e GOARCH=${_goarch} -e CGO_ENABLED=0"
    [ -n "$_goarm" ] && _envs="$_envs -e GOARM=${_goarm}"
    [ -n "$_gomips" ] && _envs="$_envs -e GOMIPS=${_gomips}"

    echo "==> building $_out (GOARCH=$_goarch GOARM=${_goarm:-} GOMIPS=${_gomips:-})"
    # shellcheck disable=SC2086
    docker run --rm -v "$ROOT":/app -w /app $_envs \
        -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
        golang:1.23-alpine \
        go build -trimpath -ldflags='-s -w' -o "$_out" ./cmd/wellboard
}

build_one aarch64_cortex-a53   arm64
build_one aarch64_generic       arm64
build_one x86_64                amd64
build_one arm_cortex-a7_neon-vfpv4 arm 7
build_one mipsel_24kc           mipsle "" softfloat

echo
echo "==> file types:"
file .build/wellboard-*
