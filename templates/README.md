# Rule template catalog (FR-5.1, FR-5.3, initial TZ Appendix В).

Each `*.yaml` in this directory is one shipped template: `id`, `name`,
`description`, `conditions` (route condition list), `typical_target`
(DIRECT / REJECT / server / group suggestion) and, for key templates,
`providers` — local fallback domain lists (FR-5.4) in
`providers/<name>.yaml`, used by the generator when geodata categories are
unavailable. Applying a template creates a normal route (FR-5.2) that the
user can then edit freely.

## Category name verification (Phase 3)

Geosite category names follow the v2fly domain-list-community convention
(UPPERCASE). They were verified against BOTH supported geodata sources by
parsing the shipped `geosite.dat` files with a length-prefixed protobuf
scan (the parser is reproduced in `internal/geodata`):

| Template | Categories | runetfreedom | MetaCubeX |
|---|---|---|---|
| ru-direct | CATEGORY-RU, TLD-RU, PRIVATE | yes | yes |
| streaming | YOUTUBE, NETFLIX, TWITCH, SPOTIFY, DISNEY, HBO | yes | yes |
| social | INSTAGRAM, FACEBOOK, TWITTER, TIKTOK, THREADS | yes | yes |
| messengers | TELEGRAM, WHATSAPP, SIGNAL, DISCORD | yes | yes |
| ai | OPENAI, ANTHROPIC, GOOGLE-GEMINI, PERPLEXITY, GITHUB-COPILOT | yes | yes |
| dev | GITHUB, GITLAB, DOCKER, NPMJS, PYTHON*, HUGGINGFACE | yes | yes |
| games | STEAM, EPICGAMES, XBOX, PLAYSTATION, RIOT | yes | yes |
| torrents | CATEGORY-PUBLIC-TRACKER, RUTRACKER | yes | yes |
| ads-block | CATEGORY-ADS-ALL | yes | yes |
| all-vpn | (no conditions — changes the default policy) | — | — |

\* `PYPI` does NOT exist in either source; the PyPI domains live in
`PYTHON` (pypi.org, pythonhosted.org, …). `TLD-RU` in a geosite rule
matches the `.ru`/`.su`/`.moscow` TLD entries (v2fly convention).
`GEOIP,PRIVATE`/`GEOIP,RU` use the geoip side (RU country code is
present in both sources' geoip.dat).

Torrents note (initial TZ Appendix В): "by process" matching is
impossible on a router (FR-4.2), so the template matches tracker
domains — limited but honest.

## Providers (local fallback, FR-5.4)

`providers/*.yaml` are small mihomo `behavior: domain` rule-provider
payloads (honest sizes: 14–25 domains each). Sources:

- `streaming`, `messengers`, `ai`, `dev` — primary domains extracted
  from the runetfreedom `geosite.dat` release (parsed 2026-09-19), CDN
  long-tails dropped.
- `ads` — NOT an excerpt of CATEGORY-ADS-ALL (it holds ~148k domains,
  too big to vendor); it is a small well-known ad/tracker core
  (doubleclick, googlesyndication, adnxs, criteo, …) so the block
  template still does something real when geodata is down.

The generator copies the providers referenced by applied templates into
the profile output dir and emits `rule-providers:` entries pointing at
them (see internal/generator + docs/DECISIONS.md Phase 3).
