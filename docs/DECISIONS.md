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

## Phase 1 decisions and verified mihomo facts

Verified 2026-09-19 with the REAL mihomo binary
(v1.19.31 linux-amd64, fetched by `scripts/fetch-mihomo.sh` into
`bin/mihomo`); each fact was probed by running `mihomo -t -f <file>`.

| # | Fact | How verified |
|---|------|--------------|
| M1 | A profile consisting only of `proxies`/`proxy-groups`/`rules` (no `tun`/`dns`/ports/`external-controller`) passes `mihomo -t`. Empty `proxies: []` is also valid. | probe p1/p9 + all 8 golden profiles |
| M2 | `GEOSITE,<cat>` rules make mihomo auto-download `GeoSite.dat` from the network when the file is missing (`-d` dir); `GEOIP` auto-downloads `geoip.metadb`. With network access, GEOSITE profiles pass `-t` WITHOUT any pre-provisioned geodata. Offline, `-t` would fail on geodata fetch — on-router nikki ships geodata, and the CI integration test relies on network like any Go module fetch. | probes p2/p3/p6/p7, fresh-dir golden run |
| M3 | `rule-providers: {}` (empty mapping) is valid for `mihomo -t`. A `type: file` rule-provider whose `path` does NOT exist also passes `-t` (mihomo does not read provider files during config test). | probes p3/p8 |
| M4 | A rule referencing an unknown policy/group fails `-t` with `proxy [name] not found` — i.e. referential integrity must be enforced by WellBoard BEFORE apply (this is why the generator returns `*Problems` instead of emitting a broken profile). | probe p12 |
| M5 | `fallback` group whose members are only `["REJECT"]` (or `["DIRECT"]`) is valid. A REALITY proxy with an invalid `public-key` FAILS `-t` (`proxy 0: invalid REALITY public key`) — so subscription data quality is visible at validate time (FR-6.2 works as designed). | probes p11/p10 |
| M6 | Proxy/group names may contain `[`, `]`, `#`, `:`, spaces and non-ASCII (e.g. `NL-2 [Вручную]`, `rt:rt_1`, `NL-1 [WellDone] #2`) — no sanitization needed beyond YAML quoting, which yaml.v3 handles. | all golden profiles pass `-t` |

Phase 1 design decisions:

- **G1 — Referential integrity.** The generator returns a structured
  `*generator.Problems` error (route id + reason) when any route target,
  group member, or the default policy references a missing/disabled/stale
  entity (FR-4.8). No partial profile is emitted for broken states.
- **G2 — rule-providers.** Per the TZ 5.4.3 the section is emitted ONLY
  when geosite conditions exist. Phase 1 emits it as an empty mapping
  (valid for mihomo, M3): the real provider entries are the Phase 3 local
  fallback lists (FR-5.4). Phase 1 state has no provider data of its own,
  and emitting fabricated providers would violate "don't invent facts".
- **G3 — Number normalization.** state.json round-trips through
  encoding/json, which decodes numbers as float64; raw proxy ports must
  render as `port: 443`, not `4.43e+02`. The generator normalizes
  integral float64s to int (recursively) before marshalling.
- **G4 — yaml.v3 panic safety.** `yaml.Marshal` PANICS (does not return an
  error) on unencodable values (chan/func) in a raw proxy map; the
  generator recovers and returns an error (found by a test, fixed by
  `marshalProfile`).
- **G5 — Service group naming.** Route service groups are `rt:<route-id>`,
  default policy group is `rt:default`; user groups whose names start with
  `rt:` are rejected (reserved prefix). Route ids are generated by the app
  (`rt_1`…), not user input, so the prefix is collision-safe.
- **G6 — Empty member groups.** A user group with zero resolvable members
  would produce an invalid profile; the generator substitutes `DIRECT` and
  reports it in Problems (state is broken, user must fix).
- **G7 — Determinism.** Routes are emitted in ascending `order`, ties by
  id; server/group name collisions resolved with ` #2`, ` #3`…; the
  golden corpus (`test/golden/`, 9 scenarios) pins the exact bytes.
- **G8 — mihomo fetch script.** `scripts/fetch-mihomo.sh` resolves the
  latest stable release of MetaCubeX/mihomo via the GitHub API; release
  assets are named `mihomo-<os>-<arch>-v<version>.gz` (the "v" is part of
  the asset name even though the tag already has it — first 404 led to
  this correction). Binary is gitignored under `/bin/`.
- **G9 — Health endpoint method.** Phase 0 review fix: the route is
  registered as `GET /api/v1/health`; net/http then answers POST (and
  other methods) with 405 + Allow header. Covered by tests.
- **G10 — CI.** Phase 0 review fix: ci.yml comment now states honestly
  that setup-go pins 1.22 = go.mod while the docker dev image is 1.23, and
  an explicit `go build ./...` step was added between vet and test.

## Phase 2 decisions and verified facts

License and toolchain verification (2026-09-19): the mihomo repo LICENSE
file at tag v1.19.31 is **GPL-3.0** (the README says "GPL-3.0"; the task
brief said AGPL-3.0 — the actual file wins). Remnawave backend is
AGPL-3.0-only (`package.json` license field), nikki is GPL-3.0.

| # | Fact | Source |
|---|------|--------|
| C1 | mihomo `common/convert` exposes exactly `ConvertsV2Ray(buf []byte) ([]map[string]any, error)` (plus unexported helpers). It parses plain AND whole-payload-base64 share-link lists (vless/trojan/ss/vmess/hysteria/hysteria2/tuic/anytls/mieru/socks/…) in one call; zero parsed proxies → error. Verified by compiling against the module and running a probe test inside the v1.19.31 checkout. | `common/convert/converter.go:16` (v1.19.31) |
| C2 | The library pull is NOT isolated: `common/convert` imports `mihomo/log` (→ common/observable), `common/utils` (→ gofrs/uuid, golang.org/x/exp), `metacubex/sing-shadowsocks` (VerifyMethod), `metacubex/http`, `metacubex/randv2`. `go list -deps` counts 251 packages with 53 external deps — the full mihomo dependency graph lands in go.sum. | `go list -deps ./common/convert` on v1.19.31 |
| C3 | Convert output quirks (probed): `port` arrives as string (e.g. "443"); vmess JSON links give int; upstream `uniqueName` dedupes duplicate fragments with `-01` suffixes; ws-opts headers get a random `User-Agent` injected (RandUserAgent — that is a UA string inside the PROXY config, unrelated to our HTTP client UA). | probe tests in the v1.19.31 checkout |
| C4 | Remnawave response headers verified in source: `subscription-userinfo` = `upload=…; download=…; total=…; expire=…` (upload is always 0, expire unix seconds, 2099 → 0); `profile-update-interval`, `profile-title` (may be `base64:…`), `announce` (may be `base64:…`), `x-hwid-active` / `x-hwid-not-supported` / `x-hwid-max-devices-reached` / `x-hwid-limit` on 200 AND 404-style responses. | `subscription.service.ts:188-243, 360-378, 621-632`, `get-user-info.headers.ts` |
| C5 | Remnawave 404-by-response-rule exists (`STATUS_CODE_404` → HttpException E404), distinct from "subscription not found". Both are handled the same way by WellBoard: x-hwid headers decide the message (device limit vs dead link). | `response-rules.middleware.ts:142-143` |

Phase 2 design decisions:

- **H1 — HWID algorithm.** Exactly as FR-2.1:
  `HEX_UPPER(SHA256(primary_mac || salt_32B))[0:32]`, salt generated once,
  stored as `<state>/hwid` (two lines: hwid, salt; 0600/0700 in prod,
  atomic tmp+rename). `Reset()` regenerates BOTH (new device slot).
  Load() falls back to Generate() when the file is missing or malformed.
  MAC discovery: `ubus call network.device status {"name":"br-lan"}`
  (macaddr field) → first non-loopback interface (name-sorted). Dev mode:
  `WELLBOARD_DEV_MAC` env or `<state>/wellboard-dev-mac` file.
  **Bug found and fixed during testing:** the initial ubus JSON scrape
  extracted the KEY `"macaddr"` instead of its value (first-quote index
  after the key); fixed to locate the value after the colon. Unit-tested
  against real ubus output shapes.
- **H2 — HWID validation.** `hwid.ValidPattern` =
  `^[a-zA-Z0-9=-]{10,64}$` (R4). Updater re-validates the resolved HWID
  before sending; a failing HWID aborts the update round loudly.
- **S1 — Fetch strategy (R1/R2/R3/R6).** UA = `WellBoard/<ver> mihomo`.
  URL candidates when the path has no known clientType segment
  (stash|singbox|mihomo|json|v2ray-json|clash, R2): try `<base>/mihomo`
  (query preserved) FIRST, then the raw URL. Definitive failures (404,
  403, 401, hwid-limit) are returned, not retried; network errors get 2
  retries with linear backoff (2s, 4s default). Timeout 20s per request.
- **S2 — Error mapping (FR-2.4).** 404 with any x-hwid limit header →
  device-limit message + "check the link"; 404 without → dead-link
  message; 403 with limit headers → device limit, 403 without → "panel
  has no response rule for this client" (R1); 200 with
  x-hwid-not-supported → logged as our-side bug, surfaced in state.
- **S3 — Interval semantics.** `profile-update-interval` parses as
  seconds; values < 24 are treated as hours (clash convention). The
  header value only applies when the user has not pinned an interval
  (FR-1.1: header = default). Fallback 12h. Scheduler tick clamped to
  ≥ 1 minute so a hostile header cannot create a request storm.
- **M1 — Merge (5.7.5).** Identity = (name, server, port) within the
  source (source_id implicit). Matched servers keep ID + DelayMS, get a
  fresh raw map, stale cleared. Vanished servers: StaleMisses+1, Stale
  set, deleted at the 3rd consecutive miss. Resurrection keeps the ID.
  Server IDs are derived: `srv_<8 hex sha256(sourceID|name|server|port)>`
  — deterministic across reinstalls (FR-3.4).
- **M2 — Network failure policy (5.7.4/NFR-5).** Any fetch or parse
  error records `last_error` (URL redacted, guardrail 5) and leaves the
  server list untouched — verified by tests against a dead port and an
  unknown-format body.
- **C6 — mihomo as a library (license).** WellBoard imports
  `github.com/metacubex/mihomo v1.19.31` **strictly through one file**
  (`internal/convert/convert.go`) calling `convert.ConvertsV2Ray`.
  **LICENSE STATUS — OPEN QUESTION FOR THE OWNER:** mihomo is GPL-3.0
  (see verification above). Linking GPL-3.0 code into the WellBoard
  binary makes the combined work GPL-3.0 (GPL §5); shipping it under the
  project's MIT claim (Q8) is a license conflict unless
  (a) the owner relicenses WellBoard GPL-3.0 (+3.0 for future versions
  per the "or later" wording is NOT present in mihomo's LICENSE, so
  GPL-3.0-only), or (b) the converter is isolated into a separate
  GPL-licensed binary/process with a clean boundary, or (c) the link
  parsing is reimplemented from scratch (no mihomo code). The TZ told
  the agent to "use the mihomo converter, do not write your own parser"
  (FR-1.3), so the import stays; the wrapper is a single file so option
  (c) remains a one-file swap. **This must be resolved before any public
  release.** Not legal advice; the owner decides.
- **M3 — Mock server topology.** The fixture handlers live in
  `internal/subscription/testfixtures` (importable, one source of
  truth); `test/mock-remnawave` is a thin `main` that serves the same
  mux for manual runs. The /sub endpoint emulates R1 (403 for a UA
  without "mihomo") and /sub/mihomo emulates R2 (200 for any UA) so the
  client's path-candidate strategy is exercised against realistic panel
  behavior. Fixture set: YAML (2 proxies), YAML (1 proxy — vanish
  scenario), base64 link list, plain link list, 404+hwid-limit, 404
  plain, 200+max-devices, announce+title+interval, not-supported,
  connection-drop, hang, header echo.
- **M4 — State schema v2.** `model.Server.StaleMisses` added (miss
  counter; raw map stays untouched so nothing leaks into the generated
  profile). Migration v1→v2 seeds StaleMisses=1 for already-stale
  servers (they have survived ≥1 missed update). Source lifecycle
  fields (UserInfo/HWIDStatus/Announce) were in the model since Phase 1
  per the TZ 5.3 example; Phase 2 starts writing them.
- **D8 — Scheduler.** Single ticker (interval from settings, min 1
  minute), first round runs immediately after Start, manual UpdateNow is
  synchronous (API button, FR-1.4). Per-source intervals live in state
  and are re-read by the updater every round — a header-provided
  interval takes effect without a restart.

## Phase 3 decisions and verified facts

Phase 3 = routing REST API (FR-3.3/FR-4/FR-4.4/FR-4.8/FR-4.9), template
catalog + apply (FR-5), LAN device list (FR-4.4), geodata sources
(FR-5.4, Q7), e2e with the real mihomo binary. All facts below verified
2026-09-19 in the dev sandbox (docker `golang:1.23-alpine`, mihomo
v1.19.31 linux-amd64 from `scripts/fetch-mihomo.sh`).

| # | Fact | How verified |
|---|------|--------------|
| P1 | mihomo implements the REJECT policy on the HTTP mixed-port as an in-band HTTP response with status **502** (Bad Gateway), not as a dropped/RESET connection: the proxy accepts the request, matches the REJECT rule, and answers itself. The Go HTTP client sees `502` with no transport error. | e2e run: GET http://doubleclick.net/ through 127.0.0.1:17890 with the ads-block route (GEOSITE,CATEGORY-ADS-ALL + RULE-SET,ads → rt:rt_1 → REJECT) returned 502; /rules showed hitCount=1 on CATEGORY-ADS-ALL |
| P2 | mihomo's `/connections` metadata for an IP-literal destination has an EMPTY `host` field; the pair to match is `destinationIP` + `destinationPort`. `chains` lists the policy chain, e.g. `["DIRECT","rt:rt_3"]` (innermost first). The `/rules` API reports rule types as `GeoSite`/`RuleSet`/`GeoIP`/`Match` (CamelCase in the API = `RULE-SET` in profile syntax) with the rule argument in `payload` and the target group in `proxy`. | e2e /connections + /rules dumps (v1.19.31) |
| P3 | mihomo `fallback` groups need their FIRST health-check round to settle; a request matched to a fallback route in the first ~1s after startup can transiently get 502 even for a DIRECT-target route (observed ~1/8 runs; log ends at "Start initial compatible provider rt:rt_3"). Polling/retry at the client is the correct semantics, not a config bug. | 40 e2e runs: intermittent 502 on the ru-direct DIRECT check, always self-healing on retry |
| P4 | odhcpd `ubus call dhcp ipv4leases` returns MACs as bare 12-hex-digit strings WITHOUT separators (verified in odhcpd src/ubus.c handle_dhcpv4_leases); dnsmasq `/tmp/dhcp.leases` uses colon form. The lan package normalizes both to colon form. | odhcpd source + unit tests over both shapes |
| P5 | Both geodata sources' geosite.dat parse with the same length-prefixed protobuf scan; all category names used by the 10 shipped templates exist in BOTH runetfreedom and MetaCubeX (table in templates/README.md). `PYPI` does NOT exist in either (PyPI domains live in `PYTHON`). | internal/geodata ParseTags over both shipped .dat files, 2026-09-19 |
| P6 | runetfreedom `geosite.dat` CATEGORY-ADS-ALL holds ~148k domain records — vendoring an excerpt would be neither complete nor honest; e2e saw "records: 911" only because MetaCubeX's default GeoSite download (fresh `-d` dir) carries a slimmer list. | e2e log (records: 911) + README table for the full count |

Phase 3 design decisions:

- **T1 — REST surface.** `internal/api`: JSON in/out with strict
  decoding (unknown fields → 400), IDs minted server-side
  (`sub_/srv_/grp_/rt_`), single mutex serializing load-modify-save
  cycles, generator `*Problems` → 409 with the problem list,
  referential guards on delete (route target / group member) → 409
  naming the dependent objects. `GET /api/v1/profile` renders the
  generated mihomo YAML (FR-4.9 preview).
- **T2 — rule-providers are REAL file providers.** The generator emits
  one `type: file, behavior: domain, path: ./providers/<name>.yaml`
  entry per provider referenced by routes (sorted), a `RULE-SET,<name>`
  line after the route's explicit conditions, and `GenerateWithProviders`
  rejects providers without a payload file (409-class error). Providers
  ship in `templates/providers/*.yaml` (14–25 domains each, honest
  sizes; see T3). mihomo does NOT read provider files during `-t`
  (Phase 1 M3), so validation order is: validate → materialize → run.
- **T3 — compact ads list instead of the 149k category.** The ads-block
  template still carries `GEOSITE,CATEGORY-ADS-ALL` (full power when
  geodata works), but its fallback provider `ads.yaml` is a small
  well-known ad/tracker core (doubleclick, googlesyndication, adnxs,
  criteo, …), NOT an excerpt of CATEGORY-ADS-ALL (~148k entries, P6) —
  vendoring a fake subset would violate "don't invent facts". The other
  providers (streaming/messengers/ai/dev) ARE excerpts of the
  runetfreedom geosite.dat primary domains (parsed 2026-09-19, CDN
  long-tails dropped).
- **T4 — 502-as-REJECT (P1).** The e2e and any future UI check treat an
  mihomo-answered 502 on a REJECT-routed domain as the SUCCESS signal
  for the block route: the connection was accepted, the rule matched,
  the forward was refused. A 200 or a transport error would be the
  failure. Fixed in the e2e comment + assert.
- **T5 — odhcpd MAC normalization (P4).** `internal/lan` merges the
  dnsmasq lease file (authoritative; default OpenWrt runs dnsmasq for
  IPv4) with the ubus odhcpd fallback (usually empty; enriches known
  devices with the `static` flag). Bare-hex MACs are normalized to
  colon form; static leases go through UCI
  (`uci add dhcp host; …; uci commit dhcp`), dev mode returns a
  documented stub error that the API maps to `200 {"status":"simulated"}`.
- **T6 — geodata sources + category verification.** `internal/geodata`:
  runetfreedom (default, Q7) and MetaCubeX download URLs, availability
  probe (HEAD → 1-byte ranged GET fallback, never a full download), and
  a v2fly protobuf scanner (`ParseTags`) used to verify that every
  geosite category referenced by the shipped templates exists in both
  sources (P5, table in templates/README.md). Settings PATCH validates
  the geodata name against the two known sources.
- **T7 — e2e harness.** `test/e2e` (build tag `e2e`, skipped without
  bin/mihomo): apply ads-block/streaming/ru-direct templates to a state
  with a loopback socks "VPN", generate, `mihomo -t`, run mihomo with
  mixed-port + external-controller, then assert: (a) REJECT → 502 (T4),
  (b) DIRECT via default policy + GEOIP,PRIVATE route → 200 from a local
  echo origin, (c) a held connection shows up in /connections with
  chains DIRECT (P2), (d) the /rules table contains the ads + streaming
  RuleSet providers. 10/10 green runs after the P3 retry fix.

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
- **RK5.** GPL-3.0 contamination via the mihomo import (C6) — release
  blocker for the public repo until the owner picks a path.
- **RK6.** The /mihomo path strategy is verified only against the mock;
  a real panel may have `disableSubscriptionAccessByPath` (R2) or 404 on
  the path form — the plain-URL fallback covers that, but only a live
  customer link (Phase 2 acceptance, deferred to the customer-provided
  test link) proves it.
- **RK7.** The clash "hours < 24" interval heuristic (S3) is a
  convention guess; a panel sending literal small-second intervals would
  be misread as hours. Real-link testing will confirm.
