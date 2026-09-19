# WellBoard

**WellBoard** — веб-панель управления VPN-подписками для OpenWrt-роутеров.
Приложение собирает несколько подписок (Remnawave и совместимые бэкенды) и
ручные прокси-серверы в единый пул, даёт понятный веб-интерфейс для гибкой
маршрутизации (сайты, категории, устройства домашней сети — разные серверы
одновременно), отправляет HWID-заголовки для подписок с лимитом устройств,
показывает квоты и сроки, встраивает metacubexd для мониторинга и управляет
профилем mihomo через nikki (транспорт, TUN, DNS остаются на стороне nikki).
Пользователи — сам владелец роутера, семья и друзья без технического
бэкграунда; в v1 один уровень доступа (админ).

## Status

Phase 0 — project skeleton (OPE-2271). See `docs/initial-tz.md` for the full
requirements (FR-*/NFR-*) and `docs/DECISIONS.md` for verified integration
facts. Roadmap: 7 phases, one PR per phase.

## Build

The build host needs only Docker; Go itself runs in a container:

```sh
make build   # docker: golang:1.23-alpine, CGO disabled
make test
make lint
```

## Run

```sh
WELLBOARD_PORT=8090 ./wellboard
# or, in dev mode:
./wellboard --dev
```

The daemon serves `GET /api/v1/health`:

```json
{"status":"ok","app":"wellboard","version":"dev"}
```

Environment: `WELLBOARD_PORT` (default 8090). Flags: `--dev`.

## Repository layout

```
cmd/wellboard/    daemon entry point
internal/api/     HTTP handlers (health endpoint)
docs/             initial TZ, phase 0 findings, verified decisions
.github/          CI (lint + test)
```

## License

MIT — see [LICENSE](LICENSE).
