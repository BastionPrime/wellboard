# WellBoard — Совместимость (NFR-4)

Зафиксированные версии и требования, с которыми WellBoard тестировался.
Обновлять этот файл при смене опорных версий (см. «Как проверялось»).

## Кратко

| Компонент | Минимальная / тестированная версия | Статус |
|---|---|---|
| OpenWrt | 23.05.x (пакет .ipk / opkg) | тестировалось: 23.05 API-контракт (UCI, procd, uci-defaults) |
| OpenWrt | 24.10.x (пакет .ipk и .apk) | тестировалось: контракты те же; .apk собран, установка через `--allow-untrusted` |
| OpenWrt | snapshot (apk, r34693) | тестировалось: смоук в `openwrt/rootfs:x86-64` (SNAPSHOT) |
| mihomo | v1.19.31 | тестировалось: e2e (валидация `-t`, живой инстанс, /connections, откат) |
| nikki | main @ 2026-09-18 (nikkinikki-org/OpenWrt-nikki) | тестировалось: контракт путей/UCI/mixin по клону (DECISIONS N1-N7) |
| Go | 1.22+ (сборка в golang:1.23-alpine) | go.mod: `go 1.22` |

## OpenWrt 23.05 / 24.10 / snapshot

- **23.05** (opkg): WellBoard распространяется как .ipk
  (`scripts/package-ipk.sh`). Используемые контракты: procd init
  (`/etc/rc.common`, START=99), UCI (`config wellboard`), uci-defaults,
  `/lib/upgrade/keep.d`. Всё — стабильный API с 23.05.
- **24.10** (opkg ИЛИ apk): те же контракты; дополнительно собран .apk
  (`scripts/package-apk.sh`, layout apk-v2). **Важно:** .apk
  НЕподписан — установка только `apk add --allow-untrusted
  /tmp/wellboard.apk`. Подпись требует ключа/фиды (RK9).
- **snapshot** (apk): смоук-тест Фазы 6 выполнялся в docker
  `openwrt/rootfs:x86-64` (SNAPSHOT r34693): uci-defaults → enable →
  procd start → health, respawn, смена порта. Это самый свежий
  контракт; обратная совместимость 23.05/24.10 обеспечивается
  использованием только базовых UCI/procd-механизмов.
- Приёмка «устанавливается на 23.05/24.10/snapshot» на реальном
  железе — пункт MANUAL_TEST (у владельца, BPI-R4).

## mihomo

- **Тестировано: v1.19.31** (linux-amd64, `scripts/fetch-mihomo.sh`,
  e2e-тесты Фаз 1-7): валидация профиля (`mihomo -t`), живой процесс с
  mixed-port + external-controller, API /connections, /rules,
  /version, автооткат по health.
- Библиотечный импорт (парсер share-ссылок): тот же
  `github.com/metacubex/mihomo v1.19.31` в go.mod
  (`internal/convert` — только `common/convert.ConvertsV2Ray`).
- **Минимальная версия**: рассчитана на 1.18+ (правила RULE-SET,
  reality-opts, provider payload format). Жёстко не проверялось на
  более старых — nikki ставит свой mihomo; если у вас старее 1.18,
  обновите nikki. Рекомендация: mihomo ≥ 1.19.
- Геоданные: GEOSITE/GEOIP-правила требуют geodata в `-d`-каталоге
  nikki (nikki ставит их сам); при отсутствии mihomo скачивает их из
  сети (DECISIONS M2). Шаблоны WellBoard (runetfreedom по умолчанию)
  несут локальные fallback-провайдеры (FR-5.4).

## nikki

- **Тестировано: main @ 2026-09-18** (clone nikkinikki-org/OpenWrt-nikki,
  Phase 0 recon, DECISIONS N1-N7). Контракт, на который опирается
  WellBoard: пути (`/etc/nikki/profiles`, `/etc/nikki/run`, …),
  активация профиля (`uci set nikki.config.profile='file:<name>'`),
  mixin (`api_listen`, `api_secret`), `mihomo -t` для валидации,
  procd-сервис. Это — main-ветка, не релиз; если в релизе nikki
  контракт изменится (переименование опций/путей), COMPATIBILITY и
  adapter нужно обновить.
- **Минимальная версия**: не ниже сборки с поддержкой mixin UCI-секций
  (текущий контракт). Если ваш nikki старее — обновите из
  nikkinikki-org репозитория.
- Зависимость пакета WellBoard: `DEPENDS:=+ca-bundle +curl +nikki`
  (nikki тянет mihomo как свою зависимость; `+mihomo` НЕ указан
  явно — mihomo приходит транзитивно через nikki, чтобы не
  конфликтовать с версией nikki).

## Пакетные DEPENDS

```
Package: wellboard
Depends: ca-bundle, curl, nikki
```

- `ca-bundle` — TLS для HTTPS-подписок.
- `curl`/`wget` — загрузка геоданных/подписок (busybox wget входит в
  base; curl — для надёжных TLS-загрузок в скриптах).
- `nikki` — сам прокси-стек (mihomo внутри) + UCI-интеграция.
- Отдельные архитектуры: aarch64_cortex-a53, aarch64_generic, x86_64,
  arm_cortex-a7_neon-vfpv4, mipsel_24kc (см. INSTALL.md).
- `luci-app-wellboard` — опционально (пункт меню LuCI, redirect на
  :8090), Depends: luci-base.

## Как проверялось (для воспроизводимости)

- e2e: `go test -tags e2e -run 'TestPhase3E2E|TestPhase5E2E'
  ./test/e2e/` — реальный mihomo v1.19.31 (bin/mihomo, gitignored),
  включая apply → health → автооткат. Зелёные на 2026-09-20.
- Смоук в OpenWrt rootfs: см. DECISIONS-phase6 D24 (SNAPSHOT x86-64
  docker).
- Контракт nikki/Remnawave: клон-снимки Phase 0 (DECISIONS.md, разделы
  N/R/C) — строки перепроверены 2026-09-19/20.
- Юнит-тесты (полный `go test ./...`) — зелёные на 2026-09-20, ветка
  ope-2271-phase7.

## Известные несовместимости / ограничения

- .apk не подписан → `--allow-untrusted` (RK9).
- nikki main — движущаяся цель; зафиксирован снимок 2026-09-18.
- metacubexd (UI мониторинга) не версионируется в репо (fetch-скрипт,
  DECISIONS D18); не влияет на совместимость ядра.
- HWID и состояние не зависят от версий mihomo/nikki — формат
  state.json версионируется отдельно (store.CurrentVersion=2, миграции
  в internal/store).
