# Desk-check e2e Phase 3 против v1.0.4 (OPE-3740)

`test/e2e/phase3_e2e_test.go` писался до external-rules (до v1.0.4).
Каждый шаг сценария сверен с текущим кодом (`internal/api/`,
`internal/nikki/`, `internal/generator/`) и прогнан заново.
Дата проверки: 2026-10-02, ветка `main` = 3892350 (v1.0.4),
mihomo v1.19.13 (bin/mihomo, gitignored), go1.25.14.

## Таблица соответствия: шаг e2e → текущее поведение

| # | Шаг e2e (функция/строка phase3_e2e_test.go) | Текущее поведение v1.0.4 | Статус | Привязка к endpoint-коду |
|---|---|---|---|---|
| 1 | `templates.Load("../../templates")` + ручной `apply("ads-block"/"streaming"/"ru-direct")` — шаблоны применяются «как API» | Каталог грузится без изменений; API применяет шаблон так же: `handleTemplateApply` создаёт route с `Conditions: t.Conditions, Providers: t.Providers, OnUnavailable:"block"`. Тест дублирует это корректно (включая `Providers`) | актуально | `internal/api/api.go handleTemplateApply`; `internal/templates/templates.go Load/Get/ProviderNames` |
| 2 | `model.State{Version: 2, Settings{...}}` — состояние уровня Phase 3 | Схема выросла до `Version 3` (geodata split, `store.CurrentVersion=3`); миграция `migrate()` достраивает v2→v3 поля. Для `generator.Generate` версия поля не считывается — профиль идентичен. Прямое сравнение `State` с API-ответом через JSON всё же прошло бы (поля совместимы) | актуально (Version: 2 — легаси, но валидно; миграция не задействована, т.к. стейт не проходит через store) | `internal/store/store.go CurrentVersion/migrate` |
| 3 | `generator.Generate(st)` + копирование payloads провайдеров | `Generate` → `GenerateWithProviders(st, nil)`; emits RULE-SET lines + `rule-providers` entries `./providers/<name>.yaml`. Тест копирует payload из `templates/providers/` — путь совпадает (`ProviderPath`) | актуально | `internal/generator/generator.go Generate/GenerateWithProviders/ProviderPath` |
| 4 | `mihomo -t -d dir -f profile.yaml` | Без изменений: `-t` не читает payload-файлы провайдеров (Phase 1 M3) — поэтому валидна копия до запуска. Прогон зелёный | актуально | — |
| 5 | Обёртка профиля mixed-port + external-controller | Конфликтов с текущим форматом нет (генератор не пишет transport-полей) | актуально | — |
| 5a | REJECT: `doubleclick.net` → 502 | mihomo-поведение (P1/T4): REJECT на mixed-port отвечает 502 in-band. Перепроверено на v1.19.13 — 502 | актуально (комментарий обновлён: добавлена версия 1.19.13) | — |
| 5b | DIRECT через default policy + GEOIP,PRIVATE | `ru-direct` → `rt:rt_3` fallback-группа `[DIRECT, REJECT]`; DefaultPolicy → `rt:default` (`MATCH,rt:default` — подтверждено в /rules-дампе: `type:Match proxy:rt:default`) | актуально | `internal/generator/generator.go` (rt:default, fallback groups) |
| 5c/5d | /connections: held-connection chains → DIRECT | mihomo-поведение (P2): `destinationIP`+`destinationPort`, chains `["DIRECT","rt:rt_3"]` — зелёный | актуально | — |
| 6 | /rules: RuleSet ads→rt:rt_1, streaming→rt:rt_2 | Правила порождаются так же; в v1.0.4-дампе появились `size`-поля (не используются тестом) | актуально | `internal/generator/generator.go` (RULE-SET emission) |
| 7 | external rules в сценарии | **Отсутствовал** — писался до external-rules. Добавлен smoke-сценарий `TestPhase3ExternalRulesSmoke` (phase3_external_rules_test.go) | добавлен | см. ниже |

## Новый сценарий external rules (smoke-объём)

`test/e2e/phase3_external_rules_test.go`, `TestPhase3ExternalRulesSmoke`.
Чистый HTTP-стек без mihomo-бинарника (часть с `mihomo -t` включается
только при наличии `bin/mihomo`). Шаги и привязка:

| Шаг | Проверка | Код |
|---|---|---|
| 1 | GET /api/v1/external-rules: все строки, view-only для PROCESS-NAME/MATCH, no-resolve флаг, source-путь, список targets | `internal/api/api.go handleExternalRulesList`; `internal/nikki/external.go LoadExternalRules/parseRuleLine` |
| 2 | POST /api/v1/external-rules/import без target: политика `proxy-vietnam` резолвится по имени в WellBoard server `srv_1`, route создаётся DISABLED, `on_unavailable:"block"` | `handleExternalRuleImport` + `targetFromPolicy` + `serverOrGroupByName` |
| 3 | GET /routes видит импортированный; PATCH /routes/{id} включает (FR-4.8 ревалидация) | `handleRoutesList` / `handleRoutePatch` |
| 4 | Профиль содержит `DOMAIN-SUFFIX,openai.com,rt:rt_1` | `internal/generator/generator.go conditionRule` (CondDomainSuffix→DOMAIN-SUFFIX) + `ServiceGroupPrefix` |
| 5 | `mihomo -t` на профиле с импортированным маршрутом (при наличии бинарника) | — |
| 6 | Контрабандное правило → 409 (правило должно существовать в живом файле дословно) | `handleExternalRuleImport` (verbatim match) |
| 7 | import-all: 2 импорта (DST-PORT, GEOIP → DIRECT), 3 скипа (PROCESS-NAME/MATCH view-only + дубль); повтор — полный no-op (дедуп по type+value) | `handleExternalRulesImportAll` |
| 8 | Инвариант read-only: файл `/etc/nikki/run/config.yaml` (фикстура) байт-в-байт не изменён | `internal/nikki/external.go` (никогда не пишет) |

## Что обновлено

- `test/e2e/phase3_e2e_test.go`: комментарий шага 5a дополнен
  перепроверкой на v1.19.13; заголовок файла ссылается на этот
  desk-check и на новый smoke-сценарий. Логика теста не менялась —
  все шаги остались валидны против v1.0.4.
- `test/e2e/phase3_external_rules_test.go`: новый
  `TestPhase3ExternalRulesSmoke` (см. таблицу выше).

## Как проверено

```
go test -tags e2e -run TestPhase3E2E ./test/e2e/       # PASS 6.3s (mihomo v1.19.13)
go test ./test/e2e/ -run Phase3ExternalRules -v        # PASS 0.10s (+ mihomo -t OK)
go vet ./test/e2e/ && go build ./...                   # OK
gofmt -l test/e2e/                                    # пусто
```

Критерий задачи "`go test ./test/e2e/... -run Phase3` green"
выполняется: и legacy-шаги, и новый smoke-сценарий зелёные.
