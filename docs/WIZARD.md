# Wizard (первый запуск) — desk-check сценария

Версия: main @ `3892350` (v1.0.4 delta). Источник: `web/src/views/WizardView.vue` (299 строк),
сторы `web/src/stores/{sources,settings,pool}.ts`, роутер `web/src/router.ts`, сервер
`internal/api/api.go`. Прогон на dev-стенде — см. раздел «Прогон».

## Обзор

WizardView — упрощённый визард первого запуска из 3 шагов (initial TZ 7, фаза 4):
подписка → политика по умолчанию → первый шаблон. Комментарий в коде
(`WizardView.vue:10-13`) честно фиксирует упрощение: клиентской валидации URL нет —
валидирует сервер, и подсказка в UI это говорит (`wizard.hint`, `web/src/locales/ru.json`).

Маршрут `/wizard` (`web/src/router.ts:11`) **не** входит в авто-редирект: guard в
`router.ts:25-28` прямо отменяет first-run auto-redirect — визард достижим только явной
навигацией (кнопка «Запустить мастер настройки» в SettingsView,
`web/src/views/SettingsView.vue:196`). Никаких проверок «а нужен ли визард» нет:
/wizard открыт всегда, и на пустом, и на настроенном стенде (надпись кнопки «Run later
(data already present)» — `wizard.later`, en.json:251 — намекает, но UI не проверяет
данные перед входом).

Все шаги живут в одном компоненте; состояние визарда — локальный `reactive` `form`
(WizardView.vue:26-31) + `step` (ref, 1..3). Сторы источника истины:
- `sources` (`stores/sources.ts`) — список источников;
- `settings` (`stores/settings.ts`) — настройки, включая `default_policy`;
- `pool` (`stores/pool.ts`) — серверы/группы/маршруты/шаблоны.

Bootstrap-загрузка этих трёх сторов выполняется guard-ом роутера **до** монтирования
любого view (`router.ts:44-54`), так что к моменту шага 3 `pool.templates` уже загружен.

## Шаги визарда с состоянием

### Шаг 1 — «Добавьте подписку» (`step === 1`, WizardView.vue:109-120)

Поля: `form.name` (input, WizardView.vue:114), `form.url` (input с placeholder
`https://…`, WizardView.vue:118). Кнопка «Далее» → `next()` (WizardView.vue:33-36):
просто `step = min(3, step+1)`, сбрасывает `error`. **Никакой валидации на переходе нет**
— пустые поля, мусорный URL, имя без URL — всё проходит дальше.

Состояние стора: ничего не меняется; данные остаются только в `form` (локально).
Изменения применяются не по шагам, а только в `finish()` на шаге 3 (см. ниже).

### Шаг 2 — «Политика по умолчанию» (`step === 2`, WizardView.vue:122-132)

Поле: `form.policy` (select, WizardView.vue:127-130), варианты только `direct` и
`reject`. `next()` → шаг 3. Снова без валидации (и не нужна: select не даёт мусора),
но и без применения — `default_policy` в сторе/на сервере ещё не тронут.

### Шаг 3 — «Первый шаблон» (`step === 3`, WizardView.vue:134-163)

Поле: `form.template` (select, WizardView.vue:139-143) с опцией пропуска (`''`) +
кликабельные карточки (WizardView.vue:144-161). Предложены только **не отключённые**
шаблоны: `offeredTemplates = pool.templates.filter(tpl => !tpl.disabled)`
(WizardView.vue:24; B1). Клик по карточке переключает выбор (WizardView.vue:150);
карточка `all-vpn` помечена бейджем MATCH (WizardView.vue:154).

Если `pool.error` — сообщение «шаблоны недоступны» (WizardView.vue:162).

Кнопки шага 3: «Назад» (`back()`, WizardView.vue:37-40: `step = max(1, step-1)`, только
сброс `error`), «Готово» → `finish()` (WizardView.vue:91-100), «Run later» →
`router.push('/dashboard')` (WizardView.vue:171) — покидает визард **без применения
чего-либо** (form теряется при уходе с компонента).

### `finish()` — единственная точка применения (WizardView.vue:91-100)

Три действия строго по порядку, при первой ошибке — стоп (`return`), `error` показывается
под кнопками (WizardView.vue:165), визард остаётся на шаге 3:

1. `addSubscription()` (WizardView.vue:42-51): если **оба** поля name и url пусты —
   тихий пропуск (`return true`, WizardView.vue:43). Иначе `sources.create({kind:
   'subscription', name, url})` (`stores/sources.ts:30-36`): POST /api/v1/sources
   (api.go:127), при успехе серверный объект push-ится в `sources.sources`.
   Серверная валидация: имя обязательно, URL обязан начинаться с http:// или https://
   (api.go:300-317).
2. `applyPolicy()` (WizardView.vue:53-65): payload `Target` — direct/reject как
   `{type}` (ветка group — WizardView.vue:58 — фактически мёртвый код, select шага 2
   даёт только direct/reject, WizardView.vue:128-129). `settings.patch({default_policy})`
   (`stores/settings.ts:33-47`): PATCH /api/v1/settings (api.go:162,
   handleSettingsPatch — api.go:2062; default_policy валидируется `validateTarget`
   (api.go:2143-2148): server/group-цели должны существовать в состоянии).
3. `applyTemplate()` (WizardView.vue:67-89): если шаблон не выбран — пропуск
   (WizardView.vue:68). Выбор target для шаблона **весь на клиенте**:
   - `all-vpn` → всегда `{type:'direct'}` (WizardView.vue:73-75);
   - иначе `typical_target` шаблона direct/reject → `{type: typical}` (WizardView.vue:76-77);
   - иначе (typical=group/server) → первая существующая группа `pool.groups[0]`
     (WizardView.vue:78-79), а при пустом пуле групп — fallback `{type:'direct'}`
     (WizardView.vue:81).
   Вызов `pool.applyTemplate(id, target)` (`stores/pool.ts:231-243`): POST
   /api/v1/templates/{id}/apply (api.go:157, handleTemplateApply — api.go:1831).
   Сервер: отклонённые шаблоны → 409 (api.go:1852-1858); target проверяется
   `validateTarget` (api.go:925-949: server/group должны существовать, stale server —
   409); `all-vpn` меняет `settings.default_policy` вместо создания маршрута
   (api.go:1867-1880); любой другой шаблон добавляет route (api.go:1882-1897).

При полном успехе — `done=true`, экран «Всё настроено» + кнопка на dashboard
(WizardView.vue:175-179).

## Места без валидации ввода

- **Шаг 1, имя и URL** — ни клиентской валидации, ни проверки при `next()`
  (WizardView.vue:33-36). Ошибку увидит только пользователь, дойдя до «Готово»
  (api.go:305-317 вернёт `name is required` / `subscription source requires an
  http(s) url`). Задумано (wizard.hint), но UX: ошибка всплывает через 2 шага.
- **Частичный ввод шага 1 молча теряется**: условие пропуска — `!form.name || !form.url`
  (WizardView.vue:43). Если введено ТОЛЬКО имя (или только URL) без второго поля —
  `addSubscription` возвращает `true` **без создания источника и без ошибки**: поле
  просто игнорируется. Пользователь видит «Всё настроено», а подписки нет.
- **URL не проверяется на клиенте и по формату** (никакого try URL parse) — «x»
  пройдёт шаг 1 и упадёт только в finish (сервер: префикс http(s)).
- **`update_interval_sec`** визардом вообще не спрашивается — по умолчанию берётся
  серверное значение планировщика.
- **`form.name` не триммится** — «  » (пробелы) считается непустым именем и уйдёт на
  сервер как есть; сервер тоже не триммит (проверено `in.Name == ""` только, api.go:306).

## Невозможные/опасные переходы (назад, повтор, изменение данных)

- **Назад после частичного finish**: `finish()` применяет действия немедленно и
  последовательно; при ошибке на шаге 2 (applyPolicy) подписка из шага 1 **уже создана**
  (sources.create push-ит в стор, WizardView.vue:45-46). Кнопка «Назад» (шаг 3) возвращает
  к редактированию `form`; повторное «Готово» **создаст дубликат подписки** — нет ни
  дедупликации по URL в UI, ни серверной (POST /sources всегда создаёт новый source
  с новым id, api.go:330-334; в отличие от import-nikki, где мэтч по URL есть —
  api.go:367+). Аналогично с шаблоном: повторный finish → повторный route
  (проверено на стенде: два rt_1/rt_2 «Реклама и трекеры — блок»).
- **«Run later» не отменяет уже сделанное**: кнопка видна на всех шагах и просто уходит
  на /dashboard; но нажатие её ПОСЛЕ частичного finish (ошибка показана) тоже теряет
  контекст — повторный заход в визард начнёт с чистого form, а полусозданное состояние
  (подписка без политики) остаётся на сервере.
- **Изменение данных при возврате**: `back()`/`next()` ходят свободно в обе стороны
  сколько угодно раз; form живёт до ухода с роута. Нельзя «назад» после `done` (кнопок
  шагов уже нет, экран успеха); нельзя «назад» из шага 1.
- **Wizard доступен на настроенном стенде**: повторный визард на существующих данных
  (кнопка в Settings) дублирует подписку/маршрут (см. выше), а `applyPolicy` молча
  перезапишет текущую default_policy, включая ту, что была выставлена через all-vpn.
- **all-vpn + шаг 2 конфликт**: если на шаге 3 выбран all-vpn, а на шаге 2 политика
  reject — applyPolicy выставит reject, затем applyTemplate(all-vpn, direct)
  перезапишет default_policy на direct (api.go:1867-1876). Итоговое значение —
  **от шага 3, не от шага 2**; пользователь, выбравший reject, этого не увидит.
- **Витрина шага 3 vs реальный apply**: карточка показывает typical_target из каталога
  (у большинства group), но фактический target подбирается фолбэками
  (первая группа или direct) — на пустом пуле все «групповые» шаблоны реально
  применяются как direct без предупреждения.

## Прогон на dev-стенде

Стенд: сборка из main (3892350), `./wellboard --dev --no-auth`, слушает 127.0.0.1:8090;
источник — центральное зеркало недоступно из этой песочницы, использован публичный
origin github.com/BastionPrime/wellboard (main = 3892350, соответствует v1.0.4).
SPA — встроенный в бинарь bundle (web/embed.go), поэтому рендерится та же версия.
Клиентский браузер в песочнице отсутствует (Chromium недоступен), прогон сценария
выполнен через прямой прогон тех же API-вызовов, которые делает код WizardView
(fetch из `stores/*.ts`), с фиксацией ответов — ниже. Каждая команда = шаг UI.

Последовательность (эквивалент «шаг1 → Далее → шаг2 → Далее → шаг3 → Готово»):

1. Шаг 1: имя «Y», URL «https://y» → `POST /api/v1/sources` →
   `{"id":"sub_1","kind":"subscription","name":"Y","url":"https://y","enabled":true}` (201).
   В сторе `sources.sources = [sub_1]`.
2. Шаг 2: политика reject → (применяется в finish) `PATCH /api/v1/settings
   {"default_policy":{"type":"reject"}}` → 200, settings.default_policy=reject.
3. Шаг 3: шаблон ads-block → `POST /api/v1/templates/ads-block/apply
   {"target":{"type":"direct"}}` → 201 `rt_1` «Реклама и трекеры — блок», target direct,
   on_unavailable block. `pool.loadAll()` re-sync: 1 маршрут.
4. Экран «Всё настроено» (клиентское done=true).

Проверки edge-cases (те же API, что вызывает визард):

- Пустые шаги 1-2 + шаблон all-vpn: `POST /templates/all-vpn/apply {"target":{"type":"direct"}}`
  → `{"status":"applied","mode":"default-policy","template":"all-vpn"}` — default_policy
  стал direct, маршрут не создан. Поведение соответствует коду (api.go:1867-1880).
- Ошибка имени: `{"name":"","url":"https://x"}` → `{"error":"name is required"}` (400).
- Ошибка URL: `{"name":"X","url":"ftp://x"}` → `{"error":"subscription source requires
  an http(s) url"}` (400). — подтверждает: ошибка шага 1 всплывает только в finish.
- Отключённый шаблон: toggle dev → disabled, apply → 409 `template "dev" is disabled;
  enable it first`. В offeredTemplates его нет (B1), но прямой API-прогон ловит 409.
- Несуществующий target: group «nonexistent» → 409 `target group "nonexistent" not found`;
  server «nope» → 409 (validateTarget, api.go:925-949). В визарде недостижимо
  (fallback на группы/direct), но проверено для полноты.
- Повторное применение ads-block (симуляция «Назад → Готово» ещё раз): второй apply →
  201 rt_2, в состоянии 2 идентичных маршрута — дубликата нет только потому, что
  вручную подчистил (DELETE /routes/rt_1, rt_2; DELETE /sources/sub_1 — все 200,
  состояние возвращено к исходному: sources=[], routes=[], default_policy=direct).

Наблюдение по UI: шаги/переходы в коде детерминированы (step инкремент/декремент),
«Назад» на шаге 2/3 и «Далее» работают без побочных эффектов до finish; побочные
эффекты только в finish и только вперёд.

## Открытые вопросы/риски (баг-кандидаты — в тикете, не правки)

Баг-кандидаты зафиксированы отдельно от этой доки.
