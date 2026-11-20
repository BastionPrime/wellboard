# WellBoard 1.0.4 — Release Notes

Released 2026-10-02. WellBoard now actually manages nikki on the router
instead of simulating it, imports your existing rules without touching them,
and ships installable packages built with the real OpenWrt tools.

## What changed

### 1. Real nikki router adapter

Router mode no longer simulates applying a profile:

- the generated profile is written to `/etc/nikki/profiles/`;
- it is selected with `uci set nikki.config.profile=<name>` +
  `uci commit nikki`;
- nikki is restarted (`/etc/init.d/nikki restart`);
- health is verified through the mihomo external-controller (`/version`);
- if the core does not come up within 15 seconds, WellBoard automatically
  restores the profile that was selected before the apply.

The nikki mixin (`nikki.mixin`) and `run/config.yaml` are never modified —
only `nikki.config.profile` is written.

### 2. Import of your current rules (read-only)

"Routes" → "External nikki rules" → "Import all rules": every importable rule
of the active config becomes a WellBoard route, disabled by default. The live
config is only read; its sha256 is identical before and after the import, and
a repeated import does not duplicate anything. Rules that a WellBoard route
cannot express (`PROCESS-NAME`, logical rules, `MATCH`) are shown in the
table as view-only.

### 3. Authentication and web cleanups

Sign in with the router root password over ubus; CSRF header on all mutating
requests; login screen with 401 redirect. Developer-facing texts removed from
the interface; the first-run wizard is opened by button from Settings instead
of auto-redirecting; the LuCI redirect is now a real LuCI view.

### 4. Custom templates and geodata

Custom templates (`/etc/wellboard/templates`, survive sysupgrade), separate
geosite/geoip sources, custom source URLs and list additions, read-only
import of an existing nikki subscription with masked source rows.

### 5. Packages built with the real tools

`.apk` packages are built with apk-tools 3 (`apk mkpkg`, v3 format — the
previous hand-made v2 archive was rejected by apk-tools 3, so 25.12 could not
install WellBoard at all); `.ipk` with OpenWrt `ipkg-build`. `VERSION` is the
single source of the release version.

## Upgrade note

OpenWrt 25.12 (apk):

```sh
apk update
apk add --allow-untrusted /tmp/wellboard-1.0.4-r1-<arch>.apk
apk add --allow-untrusted /tmp/luci-app-wellboard-1.0.4-r1-all.apk
```

OpenWrt 23.05 / 24.10 (opkg):

```sh
opkg update
opkg install /tmp/wellboard_1.0.4_<arch>.ipk
opkg install /tmp/luci-app-wellboard_1.0.4_all.ipk
```

Packages are unsigned, hence `--allow-untrusted`. Architectures:
`aarch64_cortex-a53`, `aarch64_generic`, `x86_64`,
`arm_cortex-a7_neon-vfpv4`, `mipsel_24kc`; `luci-app-wellboard` is `all`.
State (settings, routes, HWID) is preserved on upgrade; no migration step is
required.

## After installing, verify

- `ubus call system board | jsonfilter -e '@.release.version'` — the OpenWrt
  version (25.12 → apk, 23.05/24.10 → opkg).
- `/etc/config/nikki` before and after the first "Apply" — only the
  `option profile` line changes.
- `sha256sum /etc/nikki/run/config.yaml` before and after the rules import —
  the value does not change.

## Rollback

If 1.0.4 misbehaves, downgrade the packages back to the previous version with
the same package manager commands (1.0.3 `.ipk` for 23.05/24.10; for 25.12
install an earlier `.apk` if you kept it, or remove the package with
`apk del wellboard`) and restore the profile that WellBoard had selected
before the upgrade:

```sh
uci set nikki.config.profile=<previous-profile-name>
uci commit nikki
/etc/init.d/nikki restart
```

WellBoard state itself does not need a rollback: an apply that fails returns
to the previously selected profile automatically (marked `rolled_back` in the
apply history).

## Known limitations

- Packages are unsigned (`--allow-untrusted`); there is no package repository
  — files are installed manually.
- Acceptance on a live router with a copy of the working nikki state
  (~59 rules) remains with QA: the packages are structure-verified
  (apk: `apk adbdump`; ipk: `control.tar.gz`/`data.tar.gz` contents) but were
  not yet installed on a router in this release.
- Rules that cannot be expressed as a WellBoard route are imported as
  view-only entries (see above).
