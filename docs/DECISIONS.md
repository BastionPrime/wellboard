# WellBoard — DECISIONS.md

Verified facts and design decisions, each traceable to a source. Code-level
claims about nikki / Remnawave reference the shallow clones made during
Phase 0 recon (2026-09-19): nikki = [nikkinikki-org/OpenWrt-nikki](https://github.com/nikkinikki-org/OpenWrt-nikki)
(main, pushed 2026-09-18), remnawave = [remnawave/backend](https://github.com/remnawave/backend).
The clone snapshots live at `OPE-2271-research/src/nikki/` and
`OPE-2271-research/src/remnawave/`; `docs/phase0-findings.md` is the recon
report. Line numbers below were re-verified against the clones on 2026-09-19.

## Nikki integration facts (Phase 0, verified)

Verified against the nikki clone: paths in
`nikki/files/scripts/include.sh:4-12`, profile selection in
`nikki/files/nikki.init:121-154`, mixin in `nikki/files/nikki.init:155-198`,
`nikki/files/ucode/mixin.uc`.

| # | Fact | Source |
|---|------|--------|
| N1 | Paths: `HOME_DIR=/etc/nikki`, `PROFILES_DIR=/etc/nikki/profiles`, `SUBSCRIPTIONS_DIR=/etc/nikki/subscriptions`, `RUN_DIR=/etc/nikki/run`, `RUN_PROFILE_PATH=/etc/nikki/run/config.yaml`, `PROVIDERS_DIR=/etc/nikki/run/providers`. | `include.sh:4-12` |
| N2 | Active profile is UCI option `nikki.config.profile` in the form `<type>:<id>`; for a file profile `profile:file:<name>`, nikki copies `$PROFILES_DIR/$profile_name` → `$RUN_PROFILE_PATH` (`cp -f`, nikki.init:122-133). WellBoard will write the generated profile into `/etc/nikki/profiles/` and activate it with `uci set nikki.config.profile='file:<name>'; uci commit nikki`. | `nikki.init:121-133` |
| N3 | Mixin: UCI section `nikki.mixin` carries all transport options (ports, tun, dns, `api_listen`) and is overlaid onto the profile; the file `/etc/nikki/mixin.yaml` (option `mixin_file_content`) is also merged. `nikki-proxies` / `nikki-proxy-groups` / `nikki-rules` keys are PREPENDED to the profile's own lists. → Per the TZ (5.4 item 5), the generator emits ONLY `proxies` / `proxy-groups` / `rule-providers` / `rules`; transport stays with nikki. | `nikki.init:155-198`, `mixin.yaml` |
| N4 | mihomo API: `external-controller = uci nikki.mixin.api_listen` (default `[::]:9090`), `secret = uci nikki.mixin.api_secret`. The secret is generated at install time as `random=$(awk 'BEGIN{srand(); printf "%06d", int(rand() * 1000000)}')` — a 6-digit number, NOT cryptographically strong. WellBoard READS both values, never overrides them. Risk noted below. | `mixin.uc:32,38`, `uci-defaults/init.sh:9-12` |
| N5 | Service: `/etc/init.d/nikki` (procd, `START=99`), `extra_command update_subscription`, `clear_logs`; core binary `PROG="/usr/bin/mihomo"` (nikki.init:7). | `nikki.init:1-14` |
| N6 | UI: `external-ui`/`external-ui-name`/`external-ui-url` come from `uci nikki.mixin.ui_path`/`ui_name`/`ui_url`; default `ui_path='ui'`, `ui_url` points to the zashboard CDN dist. UI dir resolves under `/etc/nikki/run/ui`. → reuse candidate for metacubexd (FR-8.2; vendoring vs download decided in Phase 5). | `mixin.uc:29-31`, `nikki.conf:38-40` |
| N7 | No dedicated "validate" command in nikki; profile testing is `$PROG -d $RUN_DIR -t` (nikki.init:239). WellBoard therefore calls `mihomo -t -f <path>` itself (FR-6.2 unchanged). | `nikki.init:236-244` |

## Remnawave backend facts (Phase 0, verified)

Verified against the remnawave clone: response-rules middleware at
`src/modules/subscription-response-rules/middleware/response-rules.middleware.ts`,
matcher at `.../services/response-rules-matcher.service.ts`.

| # | Fact | Source |
|---|------|--------|
| R1 | Response format is chosen by admin-configured subscription-response-rules matching request headers (incl. User-Agent) with operators EQUALS / NOT_EQUALS / CONTAINS / NOT_CONTAINS / STARTS_WITH / NOT_STARTS_WITH / ENDS_WITH / NOT_ENDS_WITH / REGEX / NOT_REGEX. There is NO default "UA contains mihomo → mihomo format" rule; without a match the panel returns 403 (ERRORS.FORBIDDEN). | `response-rules.middleware.ts:39-68`, `response-rules-matcher.service.ts:119-170` |
| R2 | Guaranteed mihomo config: path-based clientType override — `/api/sub/<uuid>/<clientType>` where clientType ∈ `stash\|singbox\|mihomo\|json\|v2ray-json\|clash` (REQUEST_TEMPLATE_TYPE). clientType=mihomo → responseType MIHOMO → `mihomo.generator.service.ts`. If admin set `disableSubscriptionAccessByPath`, the path is blocked (BLOCK → 403). | `request-template-type.constant.ts:1-8`, `response-rules.middleware.ts:53-56`, `response-rules-matcher.service.ts:30-37, 173-199`, `render-templates.service.ts:62-70` |
| R3 | The `mihomo` UA substring is in NEITHER `EXTENDED_CLIENTS_REGEXES` nor `JSON_SUBSCRIPTION_FALLBACK_CLIENTS` (`Happ/` IS in both). A UA like `WellBoard/<ver> mihomo` may not match any rule on a vanilla Remnawave. → Phase 2 fetch strategy: (a) try `<base>/mihomo` when the URL has no clientType segment, (b) fall back to a UA containing `mihomo` for admin-authored CONTAINS rules, (c) treat 403/404 with x-hwid headers per FR-2.4. | `subscription-template/constants/extended-clients.ts:1-28` |
| R4 | HWID headers read by the panel: `x-hwid` (validated `^[a-zA-Z0-9=-]{10,64}$`), `x-device-os`, `x-ver-os`, `x-device-model`, `user-agent`. Confirms TZ FR-2.3. | `src/common/utils/extract-hwid-headers/extract-hwid-headers.util.ts:13-33` |
| R5 | Subscription response headers: `x-hwid-active: true`, `x-hwid-not-supported: true`, `x-hwid-max-devices-reached: true`, `x-hwid-limit: true`; also `subscription-userinfo` and `announce`. Confirms TZ FR-2.4 / FR-7. | `src/modules/subscription/subscription.service.ts:199-241, 360-378, 621-632` |
| R6 | An HTTP 404 response may be a response-rule action (STATUS_CODE_404), not only "subscription not found" — handle together with x-hwid headers. | `subscription-template/constants/config-types.ts:38`, `response-rules.middleware.ts:142` |
| R7 | TZ 5.7.1 claim "Remnawave picks the format by the UA substring mihomo" is REFUTED (see R1): only admin response-rules select the format; the guarantee is path clientType. Phase 2 must request a test link from the customer to inspect the actual panel's rules. | phase0-findings.md C1 |

## Phase 0 project decisions

- **D1 — Scope.** Phase 0 delivers the Go skeleton (go.mod, cmd/wellboard,
  internal/api health handler + test, Makefile, CI, LICENSE, README,
  DECISIONS) exactly as in the initial TZ section 7. One phase = one PR.
- **D2 — Dependencies.** stdlib only in Phase 0. `gopkg.in/yaml.v3` is the
  single sanctioned dependency per TZ 5.2 and lands with the mihomo profile
  generator in Phase 1. The mihomo convert package is added in Phase 2.
- **D3 — Toolchain.** The build host has no Go; `make build/test/lint` run
  everything in `golang:1.23-alpine` (image pre-pulled) with
  `CGO_ENABLED=0` (NFR-1: cross-compilation for all OpenWrt arches), and
  GOCACHE/GOPATH inside the container (`/tmp`) so the repo tree stays clean.
  go.mod pins `go 1.22` for stable toolchain semantics; the docker image
  (1.23) satisfies it.
- **D4 — Lint.** CI lint = gofmt check + `go vet`. golangci-lint needs a
  custom image or network fetch at CI time; deferred to a later phase.
- **D5 — Health endpoint.** `GET /api/v1/health` →
  `{"status":"ok","app":"wellboard","version":"dev"}`; version is injectable
  via `-ldflags "-X main.version=..."` for releases.
- **D6 — Config.** Port from `WELLBOARD_PORT` env (default 8090, decision Q6);
  `--dev` flag parsed and logged, full DryRun behavior lands in Phase 1.
- **D7 — Docs.** `docs/initial-tz.md` is a verbatim copy of the approved TZ;
  `docs/phase0-findings.md` is the recon report; this file records verified
  facts (N1-N7, R1-R7) and decisions (D1-D7).

## Risks

- **RK1.** `nikki.mixin.api_secret` is a 6-digit pseudo-random number
  (awk `rand()`), not cryptographic. WellBoard must read it as-is (N4) but
  should never treat it as a strong secret; document in INSTALL.
- **RK2.** Remnawave format selection depends on per-panel admin rules
  (R1/R7); Phase 2 needs a real panel test link from the customer to verify
  the fallback strategy.
- **RK3.** `disableSubscriptionAccessByPath` (R2) can break the guaranteed
  path-based clientType fetch on some panels; the UA fallback (R3) is the
  only remaining option there.
- **RK4.** CI (`golang:1.23` / setup-go 1.22) has not run on GitHub yet —
  the repo's remote is a local bare repo; workflow will be exercised when
  the project moves to GitHub. Local docker runs are the current green gate.
