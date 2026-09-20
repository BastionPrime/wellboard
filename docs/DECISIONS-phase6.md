## Phase 6 decisions

- **D23 — Cross-compile, not buildroot.** No OpenWrt buildroot
  checkout is kept: the package Makefile consumes a *prebuilt*
  binary produced by `scripts/cross-compile.sh` (docker
  `golang:1.23-alpine`, `CGO_ENABLED=0`, one `GOOS/GOARCH` per
  OpenWrt profile). Verified 2026-09-20: all 5 profiles build; `file`
  reports the expected statically-linked stripped ELF for each
  (arm64 = ELF64 aarch64, arm/GOARM=7 = ELF32 ARM EABI5,
  amd64 = ELF64 x86-64, mipsle/GOMIPS=softfloat = ELF32 MIPS32 LE).
  `aarch64_cortex-a53` and `aarch64_generic` share one arm64 binary
  (the profiles differ in tuning, not ABI); identical BuildIDs
  confirm. Build/Compile in the package Makefile is a no-op that
  fails loudly if `prebuilt/wellboard` is missing.
- **D24 — Smoke test method.** `docker pull openwrt/rootfs:x86-64`
  succeeded (OpenWrt SNAPSHOT r34693, target x86/64), so the smoke
  test ran in the real rootfs, not a DESTDIR-only check. The rootfs
  lacks a booted init, so the container shell started `ubusd` +
  `procd` manually; with that the *real* flow worked end-to-end:
  `cp -a` package tree → run uci-defaults (creates `/etc/wellboard`
  0700, enables service) → `/etc/init.d/wellboard enable` (S99/K10
  symlinks in `/etc/rc.d`) → `start` → procd instance "running" with
  the exact `--state`/`--templates` flags and `WELLBOARD_PORT=8090`
  env → `GET /api/v1/health` → `{"status":"ok","app":"wellboard",
  "version":"dev"}`. Also verified: kill -9 → procd respawn;
  `uci set wellboard.main.port=8091` + restart → health answers on
  8091 and 8090 is closed; `/api/v1/templates` serves the shipped
  catalog. Limitations: no logread (no syslog object in the
  container), no real `sysupgrade`, no opkg/apk install of the .ipk
  itself (opkg is not present in the rootfs image) — the tree the
  package installs is what was copied in, byte-for-byte (same
  staging code path as `scripts/package-ipk.sh`).
- **D25 — .ipk/.apk are produced by scripts, not the buildroot.**
  `scripts/package-ipk.sh` emulates the OpenWrt
  `Package/wellboard/install` layout and packs a legacy ipk
  (ar: debian-binary + control.tar.gz + data.tar.gz);
  `scripts/package-apk.sh` packs the apk-v2 tar layout with
  `.PKGINFO` (unsigned — apk needs `--allow-untrusted`, documented
  in INSTALL.md). Both are arch-parameterized; dist/ holds all 5
  arches × both formats + luci-app (arch all). The buildroot Makefile
  and the scripts intentionally install the SAME file set: the smoke
  test therefore exercises the exact tree the packages contain.
- **D26 — LuCI app is a menu redirect only.** The WellBoard UI is
  its own SPA on :8090, so `luci-app-wellboard` ships just
  `menu.d/luci-app-wellboard.json` (Services → WellBoard →
  "Open Web UI"), a `view/wellboard/redirect.js` that bounces the
  browser to `http://<host>:<uci port>/`, an rpcd ACL granting
  uci read/write on `wellboard` + `rc` for the service, and a
  `LUCI_PKGARCH:=all` luci.mk Makefile. No rpcd ucode backend: the
  app does not call ubus itself.
- **D27 — keep.d.** `/lib/upgrade/keep.d/wellboard` contains
  `/etc/wellboard` (the whole state dir: HWID, subscriptions,
  routes, generated profiles, logs), matching the nikki pattern
  (nikki lists its state dirs line-by-line). Verified by grep in the
  smoke container; a real sysupgrade run is out of scope for Docker
  (no bootloader/upgrade path in a container) — Phase 6 acceptance
  defers the live sysupgrade test to the customer's BPI-R4.
- **D28 — Init script details.** procd init `START=99 STOP=10
  USE_PROCD=1` mirrors nikki. UCI → binary mapping: port is an env
  var (`WELLBOARD_PORT`, the binary's only port knob), state and
  templates dirs are flags. `respawn` uses procd defaults with
  `respawn_retry=0` (procd's unlimited-retry convention).
  `reload_service` = stop+start (no SIGHUP handler in the Go binary).
  A `status()` override prints the `/api/v1/health` answer —
  usable even where `ubus call service list` fails.
