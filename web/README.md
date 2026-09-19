# WellBoard web (SPA)

Vue 3 + TypeScript + Vite + Pinia (initial TZ 5.2, phase 4). No external
CDNs — fonts, icons and styles are bundled (NFR).

## Layout

- `src/api.ts` — typed fetch client for the Go REST API (`/api/v1/*`,
  contract in `internal/api/api.go`).
- `src/stores/` — Pinia stores (`sources`, `settings`, `pool`).
- `src/i18n.ts` + `src/locales/{ru,en}.json` — minimal homegrown i18n
  (NFR-6), no i18n library.
- `src/views/` — one component per screen (dashboard/sources/servers/
  routes/templates/settings + first-run wizard).
- `src/App.vue` — shell: header, language toggle, nav, `<router-view>`.

## Development

Start the Go daemon first (`make run`, port 8090), then:

```
cd web
npm install
npm run dev     # vite on :5173, /api proxied to localhost:8090
```

## Build

```
npm run build   # type-checks (vue-tsc) then builds into dist/
```

`dist/` is committed (DECISIONS D13): the Go binary embeds it via
`web/embed.go` (`//go:embed all:dist`) and serves the SPA at `/` in
`--dev` mode. Hash routing needs no server fallback.

## Tests

```
npm run test    # vitest: store tests with mocked fetch
```

## Notes

- The phase 3 API has no "refresh subscription now" and no per-server
  delay-test endpoint; the UI shows last measured values and says so
  (honest UI).
- The wizard is a simplified 3-step flow (subscription → default policy
  → first template), stated as such in its hint.
