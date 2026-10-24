# WellBoard — Совместимость (NFR-4)

Зафиксированные версии и требования, с которыми WellBoard тестировался.
Обновлять этот файл при смене опорных версий (см. «Как проверялось»).

## Кратко

| Компонент | Минимальная / тестированная версия | Статус |
|---|---|---|
| OpenWrt | 23.05.x (пакет .ipk / opkg) | тестировалось: 23.05 API-контракт (UCI, procd, uci-defaults) |
| OpenWrt | 24.10.x (пакет .ipk и .apk) | тестировалось: контракты те же; `.apk` собирается apk-tools 3 `apk mkpkg` (формат v3) |
| OpenWrt | snapshot (apk, r34693) | тестировалось: смоук в `openwrt/rootfs:x86-64` (SNAPSHOT) |
| OpenWrt | 25.12.x (apk-tools 3) | контракты службы те же; `.apk` теперь собирается форматом v3 (`apk mkpkg`), install-проверка на 25.12 rootfs ещё не проведена (RK12) |
| mihomo | v1.19.31 | тестировалось: e2e (валидация `-t`, живой инстанс, /connections, откат) |
| nikki | main @ 2026-09-18 (nikkinikki-org/OpenWrt-nikki) | тестировалось: контракт путей/UCI/mixin по клону (DECISIONS N1-N7) |
| Go | 1.22+ (сборка в golang:1.23-alpine) | go.mod: `go 1.22` |
| ubus | любой OpenWrt ≥ 18.06 (`session login` через rpcd) | тестировалось: контракт `session`-объекта (пример в документации OpenWrt) |

## Сверка заявленных бэкендов с кодом

Каждая строка ниже прослеживается к конкретному файлу и строке в коде
WellBoard (указаны строки на момент v1.0.4, main @ 3892350).

### Подписочные бэкенды и форматы

| Заявлено | Где в коде | Статус |
|---|---|---|
| Remnawave-подписка: UA `WellBoard/<ver> mihomo`, стратегия `<base>/mihomo` + fallback на plain URL | `internal/subscription/subscription.go:43` (DefaultUserAgent), `:208-228` (mihomoPathCandidates), `:157-203` (Fetch) | подтверждено |
| Формат YAML с `proxies:` (clash/mihomo) | `internal/subscription/subscription.go:398-409` (ParseBody), `:414-435` (parseYAMLProxies) | подтверждено |
| base64-список share-ссылок и plain-список | `internal/subscription/subscription.go:405` → `internal/convert/convert.go:46-64` (Links; конвертер сам декодирует base64) | подтверждено |
| Поддерживаемые share-схемы: vless, trojan, ss, vmess, hysteria2, tuic | `internal/convert/convert.go:29-31` (SupportedSchemes); upstream-парсер: mihomo v1.19.31 `common/convert/converter.go` (case «vless», «trojan», «ss», «vmess», «hysteria2»/«hy2», «tuic») | подтверждено |
| Расширенные схемы конвертера (hysteria, ssr, socks/http(s), anytls и др.) тоже принимаются как подписка | `internal/convert/convert.go:26-28` («The underlying converter handles a superset; anything else it can parse is also accepted») | частично: в коде принято, но не тестировано (`internal/convert/convert_test.go` покрывает только 6 базовых схем) и не заявлено как гарантия — ручной ввод проверяет только список из 6 (`internal/api/api.go:573`) |
| Заголовки подписки: `subscription-userinfo`, `profile-update-interval`, `profile-title`, `announce` | `internal/subscription/subscription.go:295-309` (parseHeaders), `:317-343` (ParseUserInfo), `:348-361` (ParseIntervalSec), `:366-383` (DecodeMaybeBase64) | подтверждено |
| HWID-заголовки `x-hwid` + `x-device-os`/`x-ver-os`/`x-device-model` (FR-2.3) | `internal/subscription/subscription.go:261-270` (requestOnce), валидация формата: `internal/hwid/hwid.go:37` (ValidPattern `^[a-zA-Z0-9=-]{10,64}$`) | подтверждено |
| HWID-статусы ответа: `x-hwid-active`, `x-hwid-not-supported`, `x-hwid-max-devices-reached`, `x-hwid-limit` (FR-2.4) | `internal/subscription/subscription.go:106-115` (ResponseHeaders), `:232-252` (classifyStatus → ErrNotFound/ErrHWIDLimit/ErrHWIDNotSupported/ErrForbidden) | подтверждено |
| Другие Remnawave-совместимые панели: любой бэкенд, отдающий YAML-с-`proxies:` или список share-ссылок | `internal/subscription/subscription.go:14-15` (Parsing: YAML wins, иначе share-links), `internal/convert/convert.go:5` (thin wrapper) | подтверждено (формат-агностично; UA/путь-стратегия заточена под Remnawave R1-R3, но plain-fallback работает с любым бэкендом) |
| clientType-пути Remnawave: `stash\|singbox\|mihomo\|json\|v2ray-json\|clash` распознаются в URL и не дополняются `/mihomo` | `internal/subscription/subscription.go:117-119` (KnownClientTypes), `:222-226` | подтверждено, но только `mihomo` даёт совместимый формат; `singbox`/`json`/`v2ray-json` у WellBoard парсером не поддержаны — при таком URL парсер вернёт «unknown subscription format» |
| Обработка 404/403 как device-limit и как «нет response-rule» (R6/R1) | `internal/subscription/subscription.go:232-252`; тесты: `internal/subscription/mock_contract_test.go:188,203,219,234` | подтверждено |
| 20s таймаут, 2 ретрая с линейным backoff, сетевые ошибки не удаляют серверы (NFR-5) | `internal/subscription/subscription.go:46-50` (FetchTimeout/Retries), `:172-198`; тесты `mock_contract_test.go:487,514,534` | подтверждено |
| Автообновление: интервал из `profile-update-interval`, иначе 12 ч | `internal/subscription/subscription.go:56-58` (DefaultIntervalSec), `:348-361`; `internal/subscription/updater.go` | подтверждено |

### Контракт nikki / mihomo

| Заявлено | Где в коде | Статус |
|---|---|---|
| Пути: `/etc/nikki/profiles`, `/etc/nikki/run`, `/etc/init.d/nikki`, `/usr/bin/mihomo` | `internal/nikki/router.go:76-84` (Default*) | подтверждено |
| Активация профиля: `uci set nikki.config.profile='file:<name>'` + commit + restart | `internal/nikki/router.go:286-292` (Activate) | **частично/риск**: код пишет БЕЗ префикса `file:` (`"nikki.config.profile="+name`); тест (`router_test.go:158`) ожидает то же. По nikki.init (main) `profile` должен быть в форме `<type>:<id>`, иначе nikki выходит с «No profile/subscription selected». Заявление в этом документе соответствует плану DECISIONS N2, но фактический код ему не следует — см. «Известные несовместимости» |
| Read-only доступ к правилам nikki (`/etc/nikki/run/config.yaml`, никогда не пишет) | `internal/nikki/external.go:24` (RunConfigPath), `:47+` (LoadExternalRules — только os.ReadFile) | подтверждено |
| Валидация профиля: `mihomo -t -d <profiles> -f <path>` | `internal/nikki/router.go:261-272` (Validate) | подтверждено |
| Health: `external-controller` + `secret` из runtime-конфига, GET /version с Bearer | `internal/nikki/router.go:300-378` (apiAddr/apiSecret/apiHealth) | подтверждено; значение берётся из `run/config.yaml` (mixin на роутере его туда пишет), сам mixin-файл WellBoard не читает и не пишет |
| Контракт mixin не затрагивается: WellBoard пишет только `nikki.config.profile` | `internal/nikki/router.go:276-292`; тесты `router_test.go:154-165` (coexistence) | подтверждено |
| Импорт подписок nikki (`/etc/nikki/subscriptions`) не выполняется WellBoard | `internal/nikki/subscription.go` — только glob по profiles, subscriptions-каталог не читается | подтверждено |

### Версии и сборка

| Заявлено | Где в коде | Статус |
|---|---|---|
| mihomo v1.19.31 как библиотека (только `common/convert.ConvertsV2Ray`) | `go.mod:6`; единственный импорт: `internal/convert/convert.go:22` | подтверждено (в дереве нет других импортов metacubex/mihomo) |
| Go 1.22+, сборка в golang:1.23-alpine | `go.mod:3` (`go 1.22`); `Makefile:7` (GO_IMAGE) | подтверждено |
| Версия из корня: VERSION=1.0.4, `-X main.version` | `VERSION` (файл, `1.0.4`); `cmd/wellboard/main.go:38-40` (var version, ldflags) | подтверждено |
| `.apk` собирается apk-tools 3 `apk mkpkg` (формат v3), `.ipk` — ipkg-build | `scripts/package-apk.sh:13-15,86-100` (mkpkg); `scripts/package-ipk.sh` (ipkg-build) | подтверждено — сменилось в v1.0.4 (ранее v2-layout, отвергался apk-tools 3) |
| Пакетные DEPENDS: `+ca-bundle +curl +nikki`; mihomo транзитивно через nikki | `packaging/openwrt/Makefile:26` | подтверждено |
| procd `START=99`, uci-defaults, `/lib/upgrade/keep.d` | `packaging/openwrt/files/wellboard.init:10`; `files/uci-defaults.sh`; `Makefile:74-75` | подтверждено |
| Архитектуры: aarch64_cortex-a53, aarch64_generic, x86_64, arm_cortex-a7_neon-vfpv4, mipsel_24kc | `scripts/cross-compile.sh:38-42` | подтверждено |
| ubus `session login` (rpcd) для входа по паролю root; `option auth '0'`/`--no-auth` для сборок без rpcd | `internal/auth/ubus.go:27-30` (DefaultUbusURL `http://127.0.0.1/ubus`), `:191` (`session login`); `cmd/wellboard/main.go:59-61` (флаги auth/no-auth) | подтверждено |
| state.json: CurrentVersion=3, миграции в internal/store | `internal/store/store.go:31` (const CurrentVersion = 3), `:105+` (migrate) | частично: раздел «Известные несовместимости» ниже всё ещё говорит `store.CurrentVersion=2` — устаревшее заявление, исправлено в этом файле |

## OpenWrt 23.05 / 24.10 / snapshot

- **23.05** (opkg): WellBoard распространяется как .ipk
  (`scripts/package-ipk.sh`). Используемые контракты: procd init
  (`/etc/rc.common`, START=99), UCI (`config wellboard`), uci-defaults,
  `/lib/upgrade/keep.d`. Всё — стабильный API с 23.05.
- **24.10** (opkg ИЛИ apk): те же контракты; дополнительно собран .apk
  (`scripts/package-apk.sh`, формат v3 через `apk mkpkg`). **Важно:**
  .apk НЕподписан — установка только `apk add --allow-untrusted
  /tmp/wellboard.apk`. Подпись требует ключа/фиды (RK9).
- **snapshot** (apk): смоук-тест Фазы 6 выполнялся в docker
  `openwrt/rootfs:x86-64` (SNAPSHOT r34693): uci-defaults → enable →
  procd start → health, respawn, смена порта. Это самый свежий
  контракт; обратная совместимость 23.05/24.10 обеспечивается
  использованием только базовых UCI/procd-механизмов.
- Приёмка «устанавливается на 23.05/24.10/snapshot» на реальном
  железе — пункт MANUAL_TEST (у владельца, BPI-R4).

## OpenWrt 25.12 (apk-tools 3)

- **Менеджер пакетов:** в 25.12 `apk` (apk-tools 3.x) — штатный;
  `opkg` отсутствует.
- **Формат пакета:** начиная с v1.0.4 `scripts/package-apk.sh`
  собирает `.apk` через настоящий apk-tools 3 `apk mkpkg` — формат v3,
  тот, что apk-tools 3 устанавливает. Ранее собиравшийся вручную
  v2-layout (`.PKGINFO` в корне tar) отвергался с
  `v2 package format error` (проверено на rootfs
  `openwrt/rootfs:x86_64-25.12-SNAPSHOT`, r33249, apk-tools 3.0.5;
  это была причина перехода на mkpkg, DECISIONS D25).
  Установка v3-пакета на живой 25.12 rootfs ещё не проверена (RK12):
  ```sh
  docker run --rm -v /tmp:/tmp openwrt/rootfs:x86_64-25.12-SNAPSHOT \
    sh -c 'apk --version; apk add --allow-untrusted /tmp/wellboard-x86_64.apk'
  ```
- **Контракты** пакета от версии не зависят и в 25.12 не менялись:
  UCI (`/etc/config/wellboard`), procd (`/etc/init.d/wellboard`,
  `START=99`), `/etc/uci-defaults`, `/lib/upgrade/keep.d`.
- **Вход по паролю root** опирается на объект `session` в ubus
  (`session login`), который предоставляет `rpcd` (входит в base
  OpenWrt). Интерфейс тот же в 23.05/24.10/25.12; при выключенном
  `rpcd` вход вернёт `503`, для таких сборок есть
  `option auth '0'` / `--no-auth`.
- **Вход проверен живьём на 25.12** (тот же rootfs): подняты `ubusd` и
  `rpcd` из образа, пароль root задан в `/etc/shadow`, бинарник запущен
  в prod-режиме. Результат: `GET /api/v1/state` без сессии → `401`;
  неверный пароль → `401 invalid password`; верный пароль → `200` +
  `csrf` (проверка через реальный `ubus call session login`); запрос с
  CSRF-заголовком выполняется, без него и с чужим `Origin` → `403`;
  logout → `204` и `401` после него. Лог приложен к задаче.
- **Геоданные** (DECISIONS 25.12): `runetfreedom` по умолчанию,
  `metacubex` как альтернатива — `internal/geodata/geodata.go:34,45`.

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
  RULE-SET эмитит генератор (`internal/generator/generator.go:416-428`
  для локальных fallback-провайдеров), reality-opts проходит
  через сырые proxy-мапы и отвергается `mihomo -t` при битом ключе
  (`test/e2e/phase5_e2e_test.go:213-260`).
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
  Код по контрактам: пути — `internal/nikki/router.go:76-84`;
  активация — `router.go:286` (см. оговорку в таблице выше: префикс
  `file:` в текущем коде не ставится); `api_listen`/`api_secret` —
  читаются косвенно из `run/config.yaml` (`router.go:316-347`),
  mixin WellBoard не трогает; `mihomo -t` — `router.go:261`.
- **Минимальная версия**: не ниже сборки с поддержкой mixin UCI-секций
  (текущий контракт). Если ваш nikki старее — обновите из
  nikkinikki-org репозитория.
- **Зависимость пакета WellBoard: `DEPENDS:=+ca-bundle +curl +nikki`**
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
- Проверка 25.12 (apk-tools 3), 01.10.2026: docker
  `openwrt/rootfs:x86_64-25.12-SNAPSHOT` (r33249, apk-tools 3.0.5) —
  `apk add --allow-untrusted` для старого v2-пакета давал
  `v2 package format error`; после перехода на `apk mkpkg` (v1.0.4)
  формат v3, но install-проверка на 25.12 ещё не проведена (RK12);
  `ubusd`/`rpcd` в rootfs поднимаются, объект `session` в `ubus list`
  присутствует — бэкенд входа по паролю root доступен, как в
  23.05/24.10. Лог приложен к задаче WellBoard 25.12.
- Контракт nikki/Remnawave: клон-снимки Phase 0 (DECISIONS.md, разделы
  N/R/C) — строки перепроверены 2026-09-19/20; nikki.init (main)
  перепроверен 2026-10-02 при сверке COMPATIBILITY.md (ветка
  filler-compatibility-backends-audit).
- Юнит-тесты (полный `go test ./...`) — зелёные на 2026-09-20, ветка
  ope-2271-phase7.

## Известные несовместимости / ограничения

- .apk не подписан → `--allow-untrusted` (RK9). Формат — v3
  (`apk mkpkg`, v1.0.4+); установка на живой 25.12 rootfs ещё не
  проверена (RK12). Сборка через apk-tools 3 и подключение фида —
  предмет отдельной задачи релиза.
- **Активация nikki-профиля**: код (`internal/nikki/router.go:286`)
  пишет `nikki.config.profile=<name>` без префикса `file:`, тогда как
  nikki.init (main @ 2026-09) ожидает `profile` в форме
  `<type>:<id>` и при значении без типа выходит с
  «No profile/subscription selected». Заявление
  `uci set nikki.config.profile='file:<name>'` из раздела nikki —
  целевой контракт (DECISIONS N2), которому код на момент v1.0.4 не
  следует; интеграционная проверка на реальном роутере не проводилась.
  Сверка заявленного с фактическим кодом зафиксирована в таблице
  выше.
- Вход требует работающего `rpcd` (объект `session` в ubus); на
  сборках без него вход отключается через `option auth '0'`.
- nikki main — движущаяся цель; зафиксирован снимок 2026-09-18.
- metacubexd (UI мониторинга) не версионируется в репо (fetch-скрипт,
  DECISIONS D18); не влияет на совместимость ядра.
- HWID и состояние не зависят от версий mihomo/nikki — формат
  state.json версионируется отдельно (store.CurrentVersion=3,
  миграции в internal/store).
- Подписочные форматы `singbox`/`json`/`v2ray-json` (clientType-пути
  Remnawave) WellBoard не парсит: URL с таким сегментом fetched как
  есть и завершится ошибкой «unknown subscription format»
  (`internal/subscription/subscription.go:398-409`). Работают
  `mihomo`/`clash` (YAML) и списки share-ссылок.
