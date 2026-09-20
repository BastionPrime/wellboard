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

## Status: v1.0.0 (released)

Все фазы OPE-2271 (0-7) реализованы: бэкенд + SPA, apply/rollback с
автооткатом, мониторинг, пакетирование для OpenWrt, аудит безопасности
(`docs/AUDIT-phase7.md`), экспорт/импорт состояния.

Релиз `v1.0.0` опубликован (тег `v1.0.0`). Прежний блокатор —
лицензионный вопрос из-за импорта `mihomo` (GPL-3.0) — закрыт
решением владельца от 20.09.2026: WellBoard лицензируется под
**GPL-3.0** (см. секцию [License](#license)).

## Возможности

- **Подписки и серверы (FR-1, FR-3):** несколько подписок
  одновременно, единый пул серверов, ручное добавление вставкой
  share-ссылок (`vless://`, `trojan://`, `ss://`, `vmess://`,
  `hysteria2://`, `tuic://`), автообновление по расписанию, квоты и
  срок действия.
- **HWID (FR-2):** генерация по стандарту Remnawave/Happ
  (SHA256(MAC + salt)), отправка заголовков с каждым запросом
  подписки, статусы лимита устройств, регенерация. Переживает
  sysupgrade (`/lib/upgrade/keep.d/wellboard`).
- **Маршрутизация (FR-4):** правила по доменам/категориям
  (geosite/geoip)/IP/портам и **устройствам LAN**; группы серверов;
  политика по умолчанию; шаблоны в один клик (FR-5) — «Реклама и
  трекеры», «Стриминги», «AI-сервисы», «Мессенджеры» и др.
- **Применение с защитой (FR-6):** изменение → «Применить» →
  валидация `mihomo -t` → перезапуск nikki → health-check →
  **автоматический откат**, если что-то не поднялось. История 5
  профилей, кнопка «Откатить».
- **Экспорт/импорт (FR-6.5):** состояние в JSON (HWID не
  экспортируется), перенос между роутерами.
- **Мониторинг (FR-8):** metacubexd встроен (прокси к mihomo API),
  живые соединения, задержки, диагностика, логи (FR-9).
- **UI:** SPA (Vue 3), RU/EN, удобен на телефоне.

## Скриншоты

> **TODO (владелец/релиз):** сделать снимки экранов на BPI-R4 и
> положить в `docs/screenshots/`. Структура секции:
>
> | Экран | Что показывает | Файл |
> |---|---|---|
> | Дашборд | статус VPN, подписки, квоты, активные маршруты | `docs/screenshots/dashboard.png` |
> | Шаблоны | каталог шаблонов маршрутов, применение в один клик | `docs/screenshots/templates.png` |
> | Маршруты | список правил, условия, цели, порядок | `docs/screenshots/routes.png` |
> | Устройства | LAN-устройства, фиксированные IP | `docs/screenshots/devices.png` |
> | Мониторинг | metacubexd: соединения, задержки | `docs/screenshots/monitoring.png` |
> | Применение | apply/rollback, история профилей | `docs/screenshots/apply.png` |

## Установка

Установка на OpenWrt (.ipk/.apk, зависимости, UCI, автозапуск) — см.
**[docs/INSTALL.md](docs/INSTALL.md)**.

Сборка из исходников:

```sh
make build   # docker: golang:1.23-alpine, CGO disabled
make test    # юнит-тесты
make lint
```

Запуск в dev-режиме (локальный стенд с реальным mihomo):

```sh
./wellboard --dev            # слушает 127.0.0.1:8090
```

## Конфигурация

- Порт: UCI `wellboard.main.port` (env `WELLBOARD_PORT`), дефолт 8090.
- Адрес: UCI `wellboard.main.bind` (IP / имя интерфейса / пусто);
  по умолчанию — **только LAN** (`br-lan`), NFR-2.1.
- Каталог состояния: `/etc/wellboard` (0700), `/etc/config/wellboard`.

## Совместимость

OpenWrt 23.05 / 24.10 / snapshot; mihomo v1.19.31; nikki main
2026-09-18 — детали и пакетные зависимости:
**[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)**.

## Приёмка у владельца

Ручной чек-лист сценария «семья» на BPI-R4:
**[docs/MANUAL_TEST.md](docs/MANUAL_TEST.md)** (status: pending).

## Repository layout

```
cmd/wellboard/    daemon entry point (flags: --state/--templates/--bind/--dev)
internal/api/     HTTP handlers: REST CRUD, apply/rollback, export/import, monitoring
internal/generator/  state → mihomo profile YAML (rule injection guarded)
internal/nikki/   nikki adapter (UCI/procd) + dry-run dev adapter
internal/store/   atomic state.json persistence (0600/0700 in prod)
web/              embedded SPA (Vue 3)
templates/        route templates + local rule-provider payloads
packaging/openwrt/  buildroot-style package, procd init, UCI, keep.d, luci-app
docs/             TZ, DECISIONS (per-phase), AUDIT, INSTALL, COMPATIBILITY, MANUAL_TEST
test/e2e/         e2e with real mihomo (-tags e2e)
```

## Security

Аудит Фазы 7 (права файлов, LAN-only, лимиты входа, санитизация YAML,
auth/CSRF статус): **[docs/AUDIT-phase7.md](docs/AUDIT-phase7.md)**.
Важно: аутентификация в dev-контуре отсутствует (план — ubus session
login, `docs/DECISIONS-phase7.md` D31); UI слушает только LAN по
умолчанию — не выставляйте `bind=''` (все интерфейсы) без firewall и
auth.

## License

**GPL-3.0** — см. [LICENSE](LICENSE) (полный текст).

WellBoard лицензируется под GNU General Public License v3.0 — это
прямое следствие зависимости: проект импортирует
`github.com/metacubex/mihomo` (GPL-3.0) в `internal/convert`
(парсер share-ссылок, см. `docs/DECISIONS.md` RK5/C6). Собственный
код WellBoard распространяется на условиях GPL-3.0; mihomo остаётся
под своей лицензией и не перевключается. Встраивающие WellBoard в
свои продукты обязаны предоставлять исходники (GPL-3.0, разделы 4-6) —
норма OpenWrt-мира (nikki, пакеты OpenWrt).

Решение владельца от 20.09.2026 (OPE-2271, комментарий 1b2920f7):
продукт открытый, закрытых версий не планируется. Прежний план
лицензирования из Q8 ТЗ этим решением заменён на GPL-3.0.
