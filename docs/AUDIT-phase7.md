# Security Audit — Phase 7 (OPE-2271)

Аудит по фактическому коду (не по описанию), чек-лист NFR-2 / раздел 7
Фазы 7 initial-tz: auth/CSRF, права файлов, LAN-only слушание, лимиты
размера входных данных, санитизация имён серверов в YAML.
Формат: пункт → статус → файл:строка. Статусы: ✅ закрыто, ⚠️ частично
(отложено с обоснованием), ❌ отсутствует (запланировано).

Аудит проводился 2026-09-20 на ветке `ope-2271-phase7`; каждая строка
кода проверена чтением, ключевые гипотезы — пробными тестами (см.
DECISIONS-phase7 D29-D33).

## 1. Права файлов (NFR-2.3)

| Пункт | Статус | Где |
|---|---|---|
| state.json 0600 в проде | ✅ | `internal/store/store.go:29-32` (fileMode/dirMode), `store.go:149` (Chmod tmp), `store.go:173-185` (dirPerm/filePerm: 0755/0644 только в dev) |
| Каталог state 0700 в проде | ✅ | `internal/store/store.go:123` (MkdirAll с dirPerm) |
| Тест: store 0600/0700 | ✅ | `internal/store/store_test.go:144-162` (TestProdPermissions) |
| hwid файл 0600, каталог 0700 | ✅ | `internal/hwid/hwid.go:316-321` (Chmod 0600 в prod), `hwid.go:299-304` (Chmod dir 0700) |
| Тест: hwid 0600/0700 | ✅ | `internal/hwid/hwid_test.go:130-138` |
| uci-defaults/init создаёт /etc/wellboard 0700 | ✅ | `packaging/openwrt/files/uci-defaults.sh:10-13`, `wellboard.init:41-42`, `packaging/openwrt/Makefile` (INSTALL_DIR + chmod 0700) |
| Лог-файл wellboard.log | ⚠️ 0644 | `internal/applog/applog.go:42` — O_APPEND 0644. В логи не пишутся секреты (см. п. 5), но файл читаем всеми локальными пользователями. На роутере один root-пользователь; риск низкий. Отложено до prod-эскалации (см. DECISIONS-phase7 D33). |

## 2. LAN-only слушание (NFR-2.1)

| Пункт | Статус | Где |
|---|---|---|
| main.go слушал `:8090` (все интерфейсы) — на момент начала Фазы 7 | ❌→✅ | было: `cmd/wellboard/main.go:93` (`addr := ":" + port`) |
| Новое: `--bind` флаг (IP / имя интерфейса / пусто = все) | ✅ | `cmd/wellboard/main.go:57` (bindFlag), `main.go:78-113` (listenHost + ipOfInterface) |
| Прод-дефолт: br-lan (LAN-only) | ✅ | `main.go:47` (lanInterface), `main.go:101-111`: prod без --bind = первый IPv4 br-lan; нет br-lan → предупреждение + 127.0.0.1 (не WAN) |
| Dev-дефолт: loopback | ✅ | `main.go:99-100` |
| UCI-опция bind | ✅ | `packaging/openwrt/files/wellboard.init:38-46` (unset → нет флага; значение → --bind), `wellboard.config` (закомментированная опция bind с предупреждением) |
| Тесты | ✅ | `cmd/wellboard/main_test.go` (TestListenHostDefaults, TestListenHostBindFlag, TestIPOfInterfaceMissing, TestAddrForm) |

Примечание: firewall OpenWrt по умолчанию закрывает WAN для новых
соединений (input REJECT), так что `:8090` не был напрямую открыт в
интернет — но relying on firewall не соответствует NFR-2.1 буквально,
поэтому закрыто на уровне приложения.

## 3. Лимиты размера входных данных

| Пункт | Статус | Где |
|---|---|---|
| 1 MiB cap + strict decode (DisallowUnknownFields) для всех JSON-эндпоинтов | ✅ | `internal/api/api.go:157-168` (decodeStrict, http.MaxBytesReader, default 1<<20) |
| Все мутации идут через decodeStrict | ✅ | grep по api.go/monitoring.go/export.go: 12 вызовов decodeStrict (sources/servers/groups/routes/templates/lan-devices/settings/rollback/import) |
| Тест лимита | ✅ | `internal/api/export_test.go` (TestImportOversizedBody — тело >1 MiB отклоняется 400); api_coverage_test.go покрывает invalid-JSON ветки |
| Тело ответа подписки (outbound) | ✅ | `internal/subscription/subscription.go:62,287` (64 MiB cap, io.LimitReader) |
| Ответ mihomo API при диагностике | ✅ | `internal/api/monitoring.go:258` (LimitReader 1 KiB) |
| Тело mihomo-прокси | ⚠️ без лимита | `internal/api/monitoring.go:354+` (ReverseProxy) — ответ проксируется потоково; апстрим — доверенный локальный mihomo. Приемлемо. |

## 4. Санитизация имён в YAML (generator)

Проверено пробным тестом с враждебными именами (записан в
DECISIONS-phase7 D30):

| Пункт | Статус | Где |
|---|---|---|
| Имена прокси/групп квотируются yaml.v3 | ✅ | yaml.v3 Marshal для map-структур: `evil: name` → `'evil: name [manual]'`, `grp: [x] {y}` → `'grp: [x] {y}'` (проба) |
| Raw-поля прокси квотируются | ✅ | `password: p"q` сериализуется корректно (проба) |
| Уникальность имён (транспорт mihomo требует) | ✅ | `internal/generator/generator.go:199-213` (usedNames + uniqueName " #2") |
| Зарезервированный префикс rt: для юзер-групп | ✅ | `generator.go:232-236` (Problems) + `api.go:571-574,650-653` (400 на входе) |
| Имена rule-providers — только [a-zA-Z0-9-] | ✅ | `generator.go:429-447` (validProviderName) + `api.go:764-776` (валидация на входе) |
| НОВОЕ (найдено аудитом): значения условий попадают в rules-строки БЕЗ квотирования — `DOMAIN,a,b,rt:rt_1` при value="a,b" | ✅ закрыто в Фазе 7 | была инъекция сегментов правила; теперь: `internal/api/api.go:749-755` (400 на входе) + `internal/generator/generator.go:493-498` (отказ генерации — страхует import/ручной state.json); тест `export_test.go` TestRouteConditionRejectsCommaColon |
| Путь rule-provider payload | ✅ | `generator.go:423-427` (ProviderPath) — имя уже валидировано charset'ом выше |

## 5. Секреты и логирование

| Пункт | Статус | Где |
|---|---|---|
| URL подписок не логируются | ✅ | `internal/subscription/updater.go:40-41,52` (Log callback «never URLs — guardrail 5»); логируется только результат merge |
| mihomo API secret не уходит в браузер | ✅ | `internal/api/monitoring.go:355-367` (Authorization заменяется серверным Bearer), `monitoring.go:311-317` (config.js без секрета) |
| Secret не хранится в UCI wellboard | ✅ | читается из nikki.mixin в рантайме (main.go / adapter) |
| HWID не в экспорте (FR-6.5) | ✅ | `internal/api/export.go` — state.json не содержит hwid (отдельный файл `internal/hwid/hwid.go:194-196`); тест `export_test.go` TestExportDoesNotContainHWID |

## 6. Auth / CSRF (NFR-2.2 / NFR-2.4)

| Пункт | Статус | Где |
|---|---|---|
| Аутентификация (ubus session login, cookie HttpOnly) | ❌ dev-режим | Не реализована. Задуманный хук есть: `internal/api/monitoring.go:42-43,58-64` (MonitorConfig.Auth, authed() — nil = открыт) — подключается только к /ui/metacubexd и /api/mihomo/*; остальные эндпоинты не гейтятся |
| CSRF-токен для мутаций | ❌ dev-режим | Отсутствует. Митигировано фактическим контуром: (а) dev-режим слушает 127.0.0.1 (см. п. 2); (б) прод-дефолт — br-lan; (в) Content-Type: application/json + strict decode — CSRF с cross-origin формой невозможен (браузер не поставит не-simple content-type без preflight), но fetch-формы с text/plain и GET-мутаций нет — все мутации POST/PATCH/DELETE с JSON-декодом. Остаётся риск простого JSON POST с чужого сайта (no-cors + text/plain отклоняется decodeStrict как invalid JSON). |
| План на прод | зафиксировано | DECISIONS-phase7 D31: ubus session login, cookie HttpOnly; SameSite=Strict уже закрывает основной CSRF-вектор для JSON-мутаций; Auth-хук в MonitorConfig — точка подключения. Эскалация: без auth не выставлять bind='' / WAN. |

## 7. Прочее (сопутствующие пункты чек-листа Фазы 7)

| Пункт | Статус | Где |
|---|---|---|
| Export/import состояния (FR-6.5) | ✅ | `internal/api/export.go` (GET /api/v1/export, POST /api/v1/import: формат, версия, strict decode, референциальная целостность через generator до записи); тесты round-trip + валидация |
| ReadHeaderTimeout (slowloris) | ✅ | `cmd/wellboard/main.go` (http.Server ReadHeaderTimeout 5s) |
| Directory traversal в раздаче UI | ✅ | `internal/api/monitoring.go` (path.Clean + префикс-чек; http.ServeFile) |
| 405 на неверный метод | ✅ | method-patterned ServeMux (Go 1.22+); тест api_test.go:97 |
| Идемпотентные ID на сервере (клиент не выбирает) | ✅ | `internal/api/api.go` newID |

## Вывод

Из чек-листа Фазы 7 закрыто всё, что код уже закрывал (права, лимиты,
квотирование имён), плюс один реальный найденный дефект (инъекция в
rules-строки) закрыт в этой фазе. LAN-only слушание переведено с
«надеемся на firewall» на явный дефолт br-lan + флаг/UCI. Auth/CSRF —
честный статус: не реализовано в dev-контуре, хук и план на прод
зафиксированы (D31), публичный релиз без auth не предполагался (WAN
закрыт по умолчанию). v1.0.0-candidate без тега — лицензионный вопрос
GPL-3.0 у импорта mihomo (эскалация у adm, RK5).
