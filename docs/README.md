# WellBoard — Documentation Index

Entry point for everything under `docs/`. Each entry says what the document
covers and when you need it. All links are relative to this directory.
Current release: **v1.0.4** (see [RELEASE-1.0.4.md](RELEASE-1.0.4.md)).

## Documents

### [INSTALL.md](INSTALL.md)
Step-by-step installation of the `wellboard` package on OpenWrt via both
package formats: `.ipk` (opkg, 23.05/24.10) and `.apk` (apk-tools 3, 24.10+/25.12).
Covers dependencies (nikki/mihomo repo), `scp` transfer of the package file,
post-install service startup and firewall notes.
Open this when installing WellBoard on a router for the first time.

### [RELEASE-1.0.4.md](RELEASE-1.0.4.md)
What changed in v1.0.4, how to install/upgrade on each package format, what
to verify after installation, and the known limitations of this release.
Read it before or after any 1.0.4 upgrade; the "what to verify" list doubles
as a post-upgrade smoke check.

### [API.md](API.md)
Reference for the HTTP API (version 1, `/api/v1`): authentication (root
password via ubus, CSRF), state and settings, sources/servers/groups/routes,
templates, geodata, nikki external rules, profile apply, logs, LAN devices.
Use it when scripting against WellBoard or debugging what the web UI sends.

### [MANUAL_TEST.md](MANUAL_TEST.md)
Owner's manual acceptance checklist for BPI-R4: first login, adding a
subscription, applying the "Streaming via NL" template, routing the TV
through a server, reliability checks, export/import backup.
Follow it to validate that an installed build works for the "family"
scenario without reading other manuals.

### [COMPATIBILITY.md](COMPATIBILITY.md)
Pinned, tested versions and requirements (OpenWrt 23.05/24.10/snapshot and
25.12 apk-tools 3, mihomo, nikki, package DEPENDS) plus known
incompatibilities and how compatibility was verified.
Check it when picking an OpenWrt/nikki/mihomo version or planning an upgrade
of the router stack.

### [AUDIT-phase7.md](AUDIT-phase7.md)
Security audit against the actual code (not the spec) for the Phase 7 NFR-2
checklist: auth/CSRF, file permissions, LAN-only listening, input size
limits, YAML server-name sanitization. Each item: status → file:line.
Read it when reviewing security posture or before claiming Phase 7 NFR-2
items as done.

### [DECISIONS.md](DECISIONS.md)
Verified facts and design decisions D1–D22, each traceable to a source
(nikki/Remnawave upstream code recon). Includes the Phase 0 scope and
architecture choices.
The reference for "why is it built this way" for the core design.

### [DECISIONS-phase6.md](DECISIONS-phase6.md)
Phase 6 decisions D23–D28: cross-compile instead of buildroot (prebuilt
binary per OpenWrt profile via `scripts/cross-compile.sh`), packaging, and
related build choices.
Open it when touching the build/packaging pipeline.

### [DECISIONS-phase7.md](DECISIONS-phase7.md)
Phase 7 hardening and v1 release decisions D29+ (continues the numbering
above): LAN-only listening via `--bind` (default `br-lan`), auth, and other
hardening choices.
Open it when working on network binding, auth, or v1 release criteria.

### [initial-tz.md](initial-tz.md)
The original product specification (ТЗ v2) and execution plan: product
goals, phases, requirements (NFRs), acceptance scenarios.
The source of truth for intended behavior, phase scope, and NFR numbering
referenced by audit and decision docs.

### [phase0-findings.md](phase0-findings.md)
Phase 0 reconnaissance of nikki and Remnawave (2026-09-19): verified facts
from reading the upstream sources (profile layout, config format, UCI
options), which resolved the "verify" warnings in the spec.
Read it before making assumptions about nikki or Remnawave internals.

## Quick scenarios

| You want to… | Start with |
| --- | --- |
| Install on a fresh router | [INSTALL.md](INSTALL.md) |
| Upgrade to / install v1.0.4 | [RELEASE-1.0.4.md](RELEASE-1.0.4.md) |
| Roll back a bad upgrade | [RELEASE-1.0.4.md](RELEASE-1.0.4.md) (limitations + what to verify), [INSTALL.md](INSTALL.md) as the baseline |
| Check security posture | [AUDIT-phase7.md](AUDIT-phase7.md) |
| Accept / smoke-test a build | [MANUAL_TEST.md](MANUAL_TEST.md) |
| Script or debug the HTTP API | [API.md](API.md) |
| Verify router/stack versions | [COMPATIBILITY.md](COMPATIBILITY.md) |
| Understand a design choice | [DECISIONS.md](DECISIONS.md) → [DECISIONS-phase6.md](DECISIONS-phase6.md) → [DECISIONS-phase7.md](DECISIONS-phase7.md) |
| Check intended behavior / NFRs | [initial-tz.md](initial-tz.md) |
| Check nikki/Remnawave facts | [phase0-findings.md](phase0-findings.md) |

RU short notes: каждый раздел выше — «о чём документ и когда он нужен»;
таблица — «быстрые сценарии» (установить/обновить/откатить/проверить
безопасность). Индекс обновляйте при добавлении новых файлов в `docs/`.
