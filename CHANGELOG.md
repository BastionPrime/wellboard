# Changelog

All notable changes to WellBoard are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.4] - 2026-10-02

### Added

- Real nikki adapter for router mode: applying a profile now actually writes the
  generated profile to `/etc/nikki/profiles/`, selects it via
  `uci set nikki.config.profile`, restarts nikki and health-checks the mihomo
  external-controller. Previously router mode only simulated activation.
  On failure the previously selected profile is restored automatically within
  the existing 15-second rollback budget; the nikki mixin and `run/config.yaml`
  are never modified.
- Read-only import of the running nikki rules: "Routes" → "External nikki
  rules" → "Import all rules" turns every importable rule of the live config
  into a disabled WellBoard route, idempotently, without touching the live
  config (its checksum is unchanged). Rules that cannot be expressed as a
  WellBoard route (`PROCESS-NAME`, logical rules, `MATCH`) are listed as
  view-only.
- Sign in with the router root password over ubus, CSRF protection on all
  mutating requests, and a web login screen with 401 redirect.
- Custom templates (create/edit/disable/delete, stored under
  `/etc/wellboard/templates` so they survive sysupgrade) and separate
  geosite/geoip sources with custom URLs and list additions.
- Import of an existing nikki subscription (read-only), with masked source
  rows in settings.
- `docs/API.md` — HTTP API reference; `docs/INSTALL.md` and
  `docs/COMPATIBILITY.md` updated for OpenWrt 25.12 (apk).
- Release one-pager `docs/RELEASE-NOTES-v1.0.4.md`.

### Changed

- The first-run wizard no longer auto-redirects; it is opened explicitly from
  Settings ("Run setup wizard").
- Developer-facing texts removed from the interface.
- Packages are built with the real tools: `.apk` via apk-tools 3 `mkpkg`
  (format v3 — the previous hand-made v2 tarball was rejected by apk-tools 3,
  which blocked 25.12 installs), `.ipk` via OpenWrt `ipkg-build`. Manual
  hand-rolled archives are gone.
- `VERSION` is now the single source for the release version.
- `luci-app-wellboard` redirect is a real LuCI view (`view.extend`) — the LuCI
  error is gone.
- The nikki version is read from the apk database.
- Rebuilt web dist.

## [1.0.3] - 2026-09-29

### Added

- "External nikki rules" section in the Routes view: a read-only view of the
  rules currently active in the nikki config (reworked hot-build feature,
  sources preserved verbatim).
- Import of a single external rule into WellBoard: the rule becomes a route,
  turned off by default; the external source is never modified.
- `GET /api/v1/external-rules` and `POST /api/v1/external-rules/import` API
  endpoints.
- Includes all 1.0.2 changes (template cards in the wizard) in one release.

## [1.0.2] - 2026-09-27

### Added

- Template cards in the wizard: the template selection step shows a card for
  each of the ten catalog templates with its description and list source
  (click to select).
- Optional `list_source` field for templates (yaml/json); all ten shipped
  catalog templates now carry a meaningful description and list source.
- The Templates view shows the list source line.

### Fixed

- `.gitignore` anchored `/dist/` to the repo root: the unanchored pattern
  also matched `web/dist` and silently dropped hashed JS bundles from
  committed releases; the dist is fully committed again.

## [1.0.1] - 2026-09-25

### Changed

- Repository hygiene for public readers: added `CONTRIBUTING.md` with the
  external-content policy, declassified internal references from the README
  and phase documents, and removed the remaining internal wording from the
  initial technical assignment document.
- `packaging/openwrt/prebuilt` is ignored.

## [1.0.0] - 2026-09-20

### Added

- Initial public release: subscription and manual-server pool, flexible
  routing (domains, geosite/geoip categories, IPs, ports, LAN devices),
  one-click route templates, HWID headers for device-limited subscriptions,
  quota and expiry display, profile generation with mihomo validation,
  apply with automatic rollback, monitoring via embedded metacubexd,
  logs, export/import of state, OpenWrt packaging, RU/EN interface.
- Security audit (`docs/AUDIT-phase7.md`); licensed under GPL-3.0 by owner
  decision of 2026-09-20.

[Unreleased]: https://github.com/BastionPrime/wellboard/compare/v1.0.4...HEAD
[1.0.4]: https://github.com/BastionPrime/wellboard/compare/v1.0.3...v1.0.4
[1.0.3]: https://github.com/BastionPrime/wellboard/compare/v1.0.2...v1.0.3
[1.0.2]: https://github.com/BastionPrime/wellboard/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/BastionPrime/wellboard/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/BastionPrime/wellboard/releases/tag/v1.0.0
