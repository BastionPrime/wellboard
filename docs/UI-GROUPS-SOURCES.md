# UI desk-check: GroupsView и SourcesView

Поверхностный прогон по коду (desk-check) двух SPA-экранов без документации:
сценарии, состояния экранов (loading/error/empty) и связи с endpoint'ами.
Файлы: `web/src/views/GroupsView.vue` (295 строк), `web/src/views/SourcesView.vue`
(223 строки), связанные store: `web/src/stores/pool.ts`, `web/src/stores/sources.ts`,
api-слой `web/src/api.ts`, роутер-загрузчик `web/src/router.ts`. Backend —
`internal/api/api.go`, генератор конфига — `internal/generator/generator.go`,
обновитель подписок — `internal/subscription/updater.go`.

Сценарии «запустить панель и кликать» здесь не выполнялись — desk-check это
чтение кода + сверка фронт ↔ бэк. i18n-ключи сверены со словарями
`web/src/locales/ru.json` / `en.json` скриптом: все используемые ключи
существуют в обоих словарях.

## Общий каркас

- SPA монтируется роутером с hash-history: `/groups` и `/sources` —
  lazy-import компонентов (`web/src/router.ts:8,10`).
- До первого монтирования любого экрана guard делает bootstrap: settings →
  locale, затем `sources.load()` и `pool.loadAll()`, обе с `.catch(() => {})` —
  ошибка bootstrap НЕ показывается пользователю (`web/src/router.ts:44-57`).
- Все HTTP-вызовы идут через `api()`: префикс `/api/v1`, CSRF-токен на
  мутирующие методы, 401 уходит в unauthorizedHandler (`web/src/api.ts:249-274`);
  тело ошибки `{error: "..."}` превращается в `APIError` с полями
  `status/problems/routes` (`web/src/api.ts:196-207`).
- Оба экрана не имеют собственного loading-индиатора для начальных данных:
  ожидается, что router-guard уже всё загрузил до монтирования.

---

# GroupsView (экран «Группы»)

## Сценарии

### 1. Просмотр списка групп

- Данные: `pool.groups` из GET `/api/v1/groups` (`web/src/stores/pool.ts:62-69`,
  роут зарегистрирован `internal/api/api.go:136-137`).
- Карточка группы: имя, бейдж типа `t('groups.type.'+g.type)`, чипы участников
  через `pool.memberLabel(m)` — метка «имя (group)» для групп, имя сервера или
  сырой id, если участник не найден (`web/src/views/GroupsView.vue:130-141`;
  `web/src/stores/pool.ts:51-55`).
- Empty: нет групп → `t('groups.noGroups')` (`GroupsView.vue:142`).
- Bootstrap: при монтировании, если `pool.groups` пуст, повторная попытка
  `pool.loadAll().catch(() => {})` — ошибки глотаются молча
  (`GroupsView.vue:79-81`).

### 2. Создание группы

- Кнопка «Добавить» раскрывает форму (state `showForm`, `GroupsView.vue:35-41`).
- Поля: имя (required, maxlength=64, `GroupsView.vue:96-99`), тип select из
  `select/url-test/fallback/load-balance` (`GroupsView.vue:18,101-106`),
  участники — чекбоксы из `memberOptions`: все группы + все серверы пула
  (`GroupsView.vue:28-33`); сам новый id ещё не существует, поэтому «self»
  исключать не нужно.
- Клиентская валидация: непустое имя (trim) и ≥1 участник
  (`GroupsView.vue:51-58`); кнопка Submit дублирует это через `:disabled`
  (`GroupsView.vue:123`).
- Отправка: `pool.createGroup()` → POST `/api/v1/groups` с телом
  `{name, type, members}` (`web/src/stores/pool.ts:261-268`). Сервер валидирует
  (validateGroupIn, `internal/api/api.go:700-727`): имя непустое; имя НЕ должно
  начинаться с зарезервированного префикса `rt:` (`api.go:705-708`); тип из
  четырёх; ≥1 участник; каждый участник — известный server/group id; группа не
  содержит себя. Успех → 201 + Group, фронт пушит её в `pool.groups`
  (`pool.ts:266-267`), форма закрывается (`GroupsView.vue:61-62`).
- Ошибка: `formError = t('groups.saveFailed', {msg})` — msg это сырая строка
  сервера (английская) (`GroupsView.vue:63-67`). Префикс локализован, текст
  сервера — нет (см. «Локализация ошибок»).
- Асинхронные поля url-test/fallback/load-balance получают health-check в
  генераторе конфига, select — нет (`internal/generator/generator.go:277-289`).

### 3. Удаление группы

- `pool.deleteGroup(id)` → DELETE `/api/v1/groups/{id}` (`pool.ts:269-272`).
- Сервер: 404 если нет; 409 `{"error":..., "routes":[...]}` если группа — цель
  какого-то маршрута (FR-подобная защита, `internal/api/api.go:813-820`,
  `routesTargeting` ищет `rt.Target.ID == id`, `api.go:831-839`); затем удаляет
  группу из members ДРУГИХ групп и саму группу (`api.go:822-827`).
- Ошибка: `error = t('groups.deleteFailed', {msg})` (`GroupsView.vue:70-77`) —
  показывается над списком (`GroupsView.vue:88`).

### 4. Редактирование группы

- Отсутствует. PATCH `/api/v1/groups/{id}` в API есть
  (`internal/api/api.go:756-795`), но UI его не вызывает — правка участников
  задумана как delete+recreate (зафиксировано в комментарии экрана
  `GroupsView.vue:7-11`, решение D16 в `docs/DECISIONS.md`).

## Состояния экрана

| Состояние | Как выглядит | file:line |
|---|---|---|
| Данные есть | список карточек | `GroupsView.vue:130-141` |
| Empty (нет групп) | `groups.noGroups` | `GroupsView.vue:142` |
| Пул пуст (нечего выбирать в members) | `groups.noPool` подсказка в форме | `GroupsView.vue:119` |
| Ошибка удаления | красная строка `error` над списком | `GroupsView.vue:88` |
| Ошибка формы создания | красная строка `formError` в форме | `GroupsView.vue:94` |
| Saving | кнопка Submit `:disabled` + `common.loading` | `GroupsView.vue:123-125` |
| Ошибка НАЧАЛЬНОЙ загрузки | **не отображается**: `pool.error` нигде не рендерится; экран выглядит как empty | `pool.ts:73-77` vs весь template `GroupsView.vue:84-144` |
| Loading начальный | индикатора нет (bootstrap ждёт до монтирования) | `router.ts:44-57` |

Замечание: повторная попытка `loadAll` на маунте делается только когда список
пуст (`GroupsView.vue:79-81`) — если bootstrap упал, экран показывает empty, а
не ошибку.

---

# SourcesView (экран «Источники»)

## Сценарии

### 1. Просмотр списка источников

- Данные: `sources.sources` из GET `/api/v1/sources` (`web/src/stores/sources.ts:18-29`;
  роут `internal/api/api.go:126-127`). Ответ содержит полный (незамаскированный)
  URL подписки (`internal/model/model.go:74`), но таблица его НЕ рендерит —
  правило секрета соблюдено на уровне UI (`SourcesView.vue:75-107`; сравни
  замаскированные rows в `/settings`, `internal/api/api.go:2048-2059`).
- Колонки: имя+kind (`sources.kind.subscription|manual`), last update, HWID,
  действия (`SourcesView.vue:76-97`). `last_error` показывается красным
  сабтекстом (`SourcesView.vue:89`).
- `last_update` выводится сырой строкой RFC3339 UTC из state
  (`SourcesView.vue:46-49`; формат пишет `internal/subscription/updater.go:137,147,157,218,226`)
  — не локализуется и не форматируется (в отличие от `fmtDate`,
  `web/src/i18n.ts:49-52`).
- HWID-колонка (`SourcesView.vue:93-96`): приоритет `limit_reached` (⚠ +
  `sources.hwidLimit`), затем `not_supported`, затем `active` → `common.yes`,
  иначе `common.none`. Статус приходит из заголовков `x-hwid-*` при обновлении
  подписки (`internal/subscription/updater.go:254-264`); до первого успешного
  обновления поле `hwid_status` = nil → всегда «нет», неотличимо от «не
  поддерживается».
- Empty: нет источников → `sources.noSources` (`SourcesView.vue:108`).
- Экран НЕ вызывает load при монтировании: рассчитывает на bootstrap
  (`router.ts:54`; в `SourcesView.vue` нет `onMounted`).

### 2. Добавление подписки

- Форма: имя (required), URL (required, placeholder https://…), интервал
  `type=number min=0 step=60` (`SourcesView.vue:57-72`).
- Отправка: `sources.create()` → POST `/api/v1/sources` с `{kind:
  'subscription', name, url, update_interval_sec: interval || undefined}`
  (`SourcesView.vue:14-32`; `web/src/stores/sources.ts:30-34`). 0 → поле
  опускается (= авто).
- Сервер валидирует (`internal/api/api.go:300-361`): имя обязательно; для
  subscription URL обязателен и обязан начинаться с `http(s)://`
  (`api.go:311-313`); `update_interval_sec` при наличии ≥ 60 (`api.go:336-339`);
  успех → 201 + Source.
- Клиентская валидация URL/интервала ОТСУТСТВУЕТ: ввод интервала 1–59 (step=60
  не мешает ручному вводу) или не-http(s) URL → 400 с английским текстом,
  который показывается через `t('sources.addFailed', {msg})`
  (`SourcesView.vue:27-31`).
- Успех: форма очищается, источник добавляется в store (`SourcesView.vue:24-26`;
  `sources.ts:32-33`). Кнопка Submit `:disabled` на время запроса
  (`SourcesView.vue:71`).

### 3. Включение/выключение источника

- Чекбокс → `toggle()` → `sources.setEnabled(id, enabled)` → PATCH
  `/api/v1/sources/{id}` `{enabled}` (`SourcesView.vue:42-44`;
  `sources.ts:40-48`; сервер `api.go:436-487`).
- **Ошибка глотается молча**: `.catch(() => {})` (`SourcesView.vue:43`) —
  сообщения нет, отката чекбокса нет (состояние в store не изменилось, но
  DOM-чекбокс остаётся как его переключил пользователь, до следующего
  ре-рендера).

### 4. Удаление источника

- `sources.remove(id)` → DELETE `/api/v1/sources/{id}` (`sources.ts:35-39`).
- Сервер (`api.go:489-521`): 404 если нет; 409 `{"error":..., "routes":[...]}`,
  если на источник ссылаются маршруты (FR-1.5 — имена мешающих маршрутов в
  теле); затем удаляет источник и его серверы из пула (`api.go:512-519`) —
  но НЕ чистит члены групп, ссылающиеся на удалённые серверы (в отличие от
  DELETE /servers/{id} и DELETE /groups/{id}, которые чистят: `api.go:651-656`,
  `api.go:822-826`).
- Ошибка в UI: любая → `t('sources.deleteConflict')` без `{msg}` и без списка
  маршрутов (`SourcesView.vue:34-40`) — поле `APIError.routes`
  (`web/src/api.ts:196-207`) отбрасывается: пользователь не видит, КАКИЕ
  маршруты мешают.
- Состояние pool store на фронт НЕ пере-синхронизируется: группы продолжают
  показывать удалённые серверы/сырые id до перезагрузки (см. слабые места).

### 5. «Обновить»

- Кнопка вызывает `sources.load()` — только перечитывает список
  (`SourcesView.vue:110-111`); endpoint'а «обновить подписку сейчас» в API нет,
  фоновые обновления делает планировщик — честно отражено в UI-заметке
  `sources.refreshNote` и в комментарии экрана (`SourcesView.vue:6-8`;
  `sources.ts:4-6`).

## Состояния экрана

| Состояние | Как выглядит | file:line |
|---|---|---|
| Данные есть | таблица | `SourcesView.vue:75-107` |
| Empty | `sources.noSources` | `SourcesView.vue:108` |
| Отключённый источник | строка `.off` (серый текст) | `SourcesView.vue:85,190-192` |
| Ошибка last_error источника | красный сабтекст в ячейке имени | `SourcesView.vue:89,197-199` |
| Ошибка добавления | красная строка `addError` | `SourcesView.vue:73` |
| Ошибка удаления (409) | `sources.deleteConflict` в `addError` | `SourcesView.vue:34-40` |
| Ошибка toggle | **не отображается** (молчаливый catch) | `SourcesView.vue:42-44` |
| Ошибка НАЧАЛЬНОЙ загрузки | **не отображается**: `sources.error` не рендерится; выглядит как empty | `sources.ts:24-26` vs весь template `SourcesView.vue:52-113` |
| Loading (кнопка «Обновить») | индикатора/busy нет; `sources.loading` не используется экраном | `sources.ts:19` vs `SourcesView.vue:111` |
| Mobile (<360px) | колонка lastUpdate скрывается | `SourcesView.vue:209-215` |

---

# Локализация ошибок (i18n)

Все статические ключи экранов существуют в обоих словарях (проверено скриптом
по `ru.json`/`en.json`): `groups.*` (title, subtitle, add, addTitle, type,
type.select/url-test/fallback/load-balance, members, membersHint, noPool,
noGroups, nameRequired, membersRequired, saveFailed, deleteFailed),
`sources.*` (title, subtitle, name, url, interval, intervalHint, lastUpdate,
hwid, hwidLimit, hwidNotSupported, never, noSources, addFailed,
deleteConflict, refreshNote, kind.subscription, kind.manual), общие
`common.loading/save/cancel/delete/add/refresh/yes/none/on/off`. Fallback
механика: ключ → текущий словарь → ru → сам ключ (`web/src/i18n.ts:24-33`).

Слабое место локализации: серверные сообщения об ошибках — английские строки
из `{"error": "..."}` (например `internal/api/api.go:313,338,702-725`), и
вставляются в локализованный шаблон как `{msg}`: `GroupsView.vue:64,75`,
`SourcesView.vue:28`. Пользователь ru-локали видит смесь: локализованный
префикс + английская причина. Полноценно локализованы только ошибки, которые
фронт формирует сам (`groups.nameRequired/membersRequired`,
`GroupsView.vue:52-57`) и `sources.deleteConflict` (без причины).
`sources.addFailed`-вариант с msg — наполовину локализован.

# Слабые места (сводно; кандидаты на баги — списком в тикете)

1. Ошибка начальной загрузки не видна ни на одном из экранов (только
   TemplatesView/WizardView рендерят `pool.error`,
   `web/src/views/TemplatesView.vue:185`, `web/src/views/WizardView.vue:162`).
2. Cross-store рассинхрон после удаления источника/группы: фронт не пере-читывает
   pool → группы продолжают показывать удалённые члены (сырые id в чипах,
   `pool.ts:51-55`), и повторная `loadAll` не помогает — в state остаются
   «висячие» id. Для DELETE /sources/{id} их создаёт сам backend: он удаляет
   серверы источника, но НЕ чистит их из members групп (`api.go:512-519`;
   сравни чистку в DELETE /servers `api.go:651-656` и DELETE /groups
   `api.go:822-826`) — до применения конфига это молчит (генератор skip'ает
   отсутствующих членов с проблемой, `generator.go:260-264`), но данные в UI
   и state рассогласованы.
3. `PATCH /api/v1/groups/{id}` НЕ вызывает validateGroupIn — можно записать
   невалидные type/members/несуществующие id (`api.go:756-795` vs
   `api.go:729-755`); UI не страдает (PATCH не используется), но API
   неконсистентен с POST.
4. Отсутствие проверки циклов групп: API допускает A∈B, B∈A (validateGroupIn
   проверяет только self, `api.go:717-725`); UI тоже не предупреждает. В
   генераторе self пропускается с проблемой, отсутствующий член — skip, пустой
   список → DIRECT (`internal/generator/generator.go:252-270`), цикл не
   детектируется — mihomo откажется на этапе применения.
5. Молчаливый catch на toggle enable (`SourcesView.vue:42-44`).
6. `APIError.routes` (мешающие маршруты при 409) не показывается
   (`SourcesView.vue:34-40`; `GroupsView.vue:70-77`).
7. Нет клиентской валидации URL/интервала в SourcesView (→ английский 400).
8. `last_update` сырой RFC3339 UTC, не локализован (`SourcesView.vue:46-49`).
9. Асимметрия валидации имени: UI режет 64 символа (`maxlength`,
   `GroupsView.vue:98`), сервер длину не проверяет (`api.go:701-703`).
