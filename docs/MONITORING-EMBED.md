# Мониторинг: встраивание metacubexd и прокси к mihomo API

Архитектурная записка FR-8 (Фаза 5). Фиксирует, откуда берётся дист
metacubexd, как он версионируется, как попадает на роутер, как
выглядит поток через прокси и какие у этого ограничения. Все ссылки
вида `file:line` указывают на main (v1.0.4, коммит 3892350); для
актуальности строк сверяйтесь с кодом.

## 1. Схема встраивания

```
Браузер (SPA WellBoard, вкладка Monitoring)
  └─ iframe /ui/metacubexd/            ← static dist (п. 2)
       └─ fetch /ui/metacubexd/config.js   ← генерируется бэкендом,
                                              НЕ из dist (п. 4)
       └─ все обращения к API идут на
          /api/mihomo/*                 ← ReverseProxy / WS-пайп (п. 5)
             → 127.0.0.1:<external-controller> (mihomo внутри nikki)
                + Authorization: Bearer <api_secret> (вставлен сервером)
```

Ключевой принцип TZ 5.9: mihomo слушает external-controller только
на loopback, браузер до него дотянуться не может, поэтому metacubexd
не ходит в API напрямую — только через прокси WellBoard, а секрет
живёт на сервере и в браузер не попадает.

Где это в коде:
- SPA-вкладка (iframe + HEAD-проба наличия dist):
  `web/src/views/MonitoringView.vue:21,25`
- Роуты: `internal/api/monitoring.go:73-75`
  (`GET /ui/metacubexd/`, `GET /ui/metacubexd`, `/api/mihomo/`).
- Конфигурация: `MonitorConfig.UIDir / MihomoAPIAddr /
  MihomoAPISecret` — `internal/api/monitoring.go:31,34,36`;
  подключается в `cmd/wellboard/main.go:234-241`.

## 2. Источник dist и версионирование

- **Репозиторий-источник:** `MetaCubeX/metacubexd`, релизный артефакт
  `compressed-dist.tgz` — `scripts/fetch-metacubexd.sh:15,35`.
- **Скрипт-загрузчик:** `scripts/fetch-metacubexd.sh`. Без аргумента
  резолвит latest через GitHub API (`.../releases/latest`,
  `scripts/fetch-metacubexd.sh:26`); с аргументом ставит явно
  указанную версию (например `v1.273.1`).
- **Проверенная версия:** v1.273.1 (проверено 2026-09-20, комментарий
  в `scripts/fetch-metacubexd.sh:11`); упоминается как рабочая и в
  `docs/DECISIONS.md:351-362` (D18).
- **Куда ставится:** `ui/metacubexd/` — каталог gitignored
  (`.gitignore:9-11`): ~8 МБ сторонних build-артефактов не
  вендорится в репо (DECISIONS D18). Версия записывается скриптом в
  `ui/metacubexd/VERSION` (`scripts/fetch-metacubexd.sh:51`) —
  это единственный след версии, по нему можно проверить, что
  установлено.
- **Проверка целостности:** только структурная — скрипт падает, если
  в архиве нет `index.html` (`scripts/fetch-metacubexd.sh:44-47`).
  Checksum/подпись upstream-релиза не проверяется (curl -fsSL по
  HTTPS). Отсутствие пиннинга — осознанное упрощение для dev-dist;
  дист не исполняется на сервере, это статика для браузера.
- **Честное отсутствие:** если дист не скачан, `/ui/metacubexd/`
  отвечает 404 с подсказкой «run scripts/fetch-metacubexd.sh»
  (`internal/api/monitoring.go:301-312`), а вкладка Monitoring
  показывает инструкцию вместо пустого iframe
  (`web/src/views/MonitoringView.vue:24-28`). Мёртвого UI нет.

## 3. Как dist попадает в пакет

**Никак — в пакеты не попадает.** Оба упаковочных скрипта
(`scripts/package-ipk.sh:46-61`, `scripts/package-apk.sh:46-56`)
кладут в `/usr/share/wellboard/` только `templates/`; каталог
`ui/metacubexd` в staging не копируется (grep по packaging/* его не
находит). Последствия:

- На установленном пакете `uiDir()` возвращает `""` (см. п. 6) —
  мониторинг-UI честно сообщает «dist not present». Это не баг, а
  дизайн D18: ~8 МБ в ipk/apk не тащим.
- Оператору, которому нужен мониторинг-UI на роутере, следует
  вручную положить dist в `/usr/share/wellboard/ui/metacubexd/`
  (см. п. 7, шаг 4) — этот fallback путь заложен в `uiDir()`.
- В dev-контуре (чеккаут репо) скрипт кладёт дист прямо в
  `ui/metacubexd/`, и сервер подхватывает его оттуда.

## 4. config.js: сгенерированный, не из dist

Shipped-дист содержит пустой `config.js` (хук самого metacubexd:
`window.__METACUBEXD_CONFIG__.defaultBackendURL`). WellBoard
перехватывает запрос `/ui/metacubexd/config.js` и отдаёт свой,
сгенерированный на лету (`internal/api/monitoring.go:329-331`,
тело — `serveMetaCubeXDConfig`, `internal/api/monitoring.go:351-368`):

```js
window.__METACUBEXD_CONFIG__ = {
  defaultBackendURL: '/api/mihomo',
  githubToken: '',
}
```

`defaultBackendURL` — same-origin путь; секрет там отсутствует
принципиально: Bearer-токен вставляет прокси на сервере
(review follow-up, FR-8/D18). Это же проверяется e2e: config.js
должен содержать `defaultBackendURL: '/api/mihomo'` и НЕ содержать
secret — `test/e2e/phase5_e2e_test.go:421-431`.

## 5. Прокси `/api/mihomo/*` → external-controller

`handleMihomoProxy` (`internal/api/monitoring.go:369`):

1. Гейт авторизации `authed()` (`internal/api/monitoring.go:58-64`):
   `MonitorConfig.Auth == nil` → открыт (dev, D19); в проде хук
   подключается тем же middleware, что и остальной API.
2. Секрет: любой клиентский `Authorization` удаляется и заменяется
   серверным `Bearer <api_secret>`
   (`internal/api/monitoring.go:380-384`) — секрет из nikki
   (`mixin.api_secret`) до браузера не доходит.
3. Путь: срезается префикс `/api/mihomo` (апстрим ждёт корневые
   `/version`, `/connections`, ...) — Director в
   `internal/api/monitoring.go:395-405`.
4. WebSocket (`/traffic`, `/logs`, `/memory` — стримы): детект
   upgrade (`isUpgrade`, `internal/api/monitoring.go:413`), затем
   hijack клиентского conn + raw TCP dial на апстрим
   (`proxyWebSocket`, `internal/api/monitoring.go:431-472`),
   рукопожатие переписывается на апстрим-Host, далее `io.Copy` в
   обе стороны.
5. Ошибки апстрима → 503 JSON через ErrorHandler
   (`internal/api/monitoring.go:406-409`).

Диагностический зонд отдельный: `probeMihomoAPI` GET `/version` с
тем же секретом и лимитом чтения 1 КиБ
(`internal/api/monitoring.go:256,275`) — это то, что показывает
«Мониторинг → Диагностика».

### Известное ограничение: стрим-ответ без лимита

Тело ответа mihomo-прокси не ограничено по размеру: ReverseProxy
проксирует ответ потоково, без `io.LimitReader`. Это зафиксировано
аудитом Фазы 7 (§3, `docs/AUDIT-phase7.md:50`): статус «⚠️ без
лимита … Приемлемо». Обоснование:

- Апстрим — доверенный локальный mihomo на 127.0.0.1 (не внешний
  источник), его ответы ограничены самим ядром (REST API возвращает
  JSON-снапшоты и WS-события, а не произвольные файлы).
- Все остальные входы лимитированы: JSON-эндпоинты 1 МиБ
  (`internal/api/api.go:214-225`, decodeStrict + MaxBytesReader),
  ответы подписки 64 МиБ (`internal/subscription/subscription.go`),
  диагностический зонд 1 КиБ (`internal/api/monitoring.go:275`).
- Ставить лимит на потоковый WS-туннель (`/traffic`, `/logs`,
  `/memory`), который по определению бесконечен, означало бы
  обрывать мониторинг, а не защищать.

Что делать при изменении угрозы: если mihomo когда-нибудь начнёт
отдавать через прокси большие конечные REST-ответы (например,
`/configs` с огромным контентом), добавить `ModifyResponse` с
`io.LimitReader` для не-WS запросов в `handleMihomoProxy`
(`internal/api/monitoring.go:395`) — точка расширения уже есть.

## 6. Где сервер ищет дист (uiDir)

`cmd/wellboard/main.go:391-402`, порядок:

1. dev-режим: `ui/metacubexd/index.html` в рабочем каталоге
   (туда ставит fetch-скрипт);
2. прод-фолбэк: `/usr/share/wellboard/ui/metacubexd/index.html`
   (сюда дист может положить оператор/пакет — см. п. 3);
3. иначе `""` → 404 с подсказкой.

Обработка запросов к дисту — `handleMetaCubeXD`
(`internal/api/monitoring.go:296`): traversal закрыт
`path.Clean("/"+rel)` (`internal/api/monitoring.go:319`, аудит
Фазы 7 §7), каталог → `index.html`, для `.html` — `Cache-Control:
no-cache` (`internal/api/monitoring.go:340`), `config.js`
перехватывается до раздачи файлов (п. 4).

## 7. Процедура обновления версии metacubexd

Обновление версии = перевыкачивание dist; код WellBoard при этом не
меняется (прокси и config.js — our own layer). Процедура:

1. Посмотреть текущую версию: `cat ui/metacubexd/VERSION` (пишется
   `scripts/fetch-metacubexd.sh:51`). Запомнить её как «откат».
2. Обновить дист:
   `scripts/fetch-metacubexd.sh v<новая-версия>` — при отсутствии
   тега скрипт упадёт на curl (404) и `ui/metacubexd/` останется
   пустым (скрипт сначала `rm -rf DEST`, затем распаковка,
   `scripts/fetch-metacubexd.sh:41-47`), так что неудачное
   обновление видно сразу: `ls ui/metacubexd/index.html`.
   Откат: повторить шаг с прежней версией.
3. Обновить комментарий в `scripts/fetch-metacubexd.sh:11`
   («verified <дата>, v<версия>») — это фактический фикс версии в
   репо (вместе с `docs/COMPATIBILITY.md:160-162`, где сказано:
   metacubexd не версионируется в репо, fetch-скрипт, D18). Коммит
   «docs/scripts: verified metacubexd v<...>» по обычному PR-циклу.
4. На роутере (если дист кладётся вручную): скачать тот же
   `compressed-dist.tgz`, распаковать в
   `/usr/share/wellboard/ui/metacubexd/`, убедиться, что появился
   `index.html`; перезапуск WellBoard не нужен — путь проверяется
   на каждый запрос через `UIDir` (статический путь, но раздача
   идёт по запросу; `os.Stat` в `handleMetaCubeXD`,
   `internal/api/monitoring.go:301-312`).

Проверка после обновления (все команды — из чеккаута репо):

1. `ls ui/metacubexd/index.html && cat ui/metacubexd/VERSION` —
   dist на месте, версия ожидаемая.
2. `make test` — юнит-тесты раздачи UI:
   `TestMetaCubeXDServing` / `TestMetaCubeXDMissing`
   (`internal/api/monitoring_test.go:283,349`) и прокси
   `TestMihomoProxyHTTP` / `TestMihomoProxyUnreachable`
   (`internal/api/monitoring_test.go:249,273`) должны быть зелёными
   (они гоняются на сгенерированном фейковом dist, реальная версия
   не влияет — но проверяют, что путь раздачи не сломан).
3. E2E с реальным mihomo: `go test -tags e2e ./test/e2e/`
   (`test/e2e/phase5_e2e_test.go:401-431`): если дист скачан —
   проверяется, что `/ui/metacubexd/` отдаёт 200 HTML, а
   `config.js` содержит `defaultBackendURL: '/api/mihomo'` и не
   содержит секрет. Без dist тест честно скипается
   (`test/e2e/phase5_e2e_test.go:119-121`).
4. Ручная проверка на стенде: открыть WellBoard → вкладка
   «Мониторинг» → iframe загружает metacubexd, endpoint-форма
  prefilled адресом (не пустая), виден live-трафик
   (WS `/api/mihomo/traffic`); «Мониторинг → Диагностика» →
   строки `mihomo` и `nikki` зелёные. Если после обновления
   metacubexd перестал читать `window.__METACUBEXD_CONFIG__`
   (проверить в исходниках их `config.js`-хука) — это точка
   совместимости, обновить `serveMetaCubeXDConfig`
   (`internal/api/monitoring.go:351`).

## 8. Чего в встраивании нет (осознанно)

- Дист не в git и не в пакетах (D18): происхождение видно из
  fetch-скрипта, репо не раздувается.
- Нет пиннинга checksum релиза metacubexd (только структурная
  проверка index.html).
- Auth на `/ui/metacubexd/` и `/api/mihomo/*` в dev открыт
  (`MonitorConfig.Auth == nil`, D19); прод-гейт — через тот же хук,
  план зафиксирован в DECISIONS-phase7 D31
  (`docs/DECISIONS-phase7.md:36-50`), детали аудита:
  `docs/AUDIT-phase7.md:80`.
- Стрим-ответ прокси без лимита — см. п. 5, обоснование аудита
  Фазы 7 §3.
