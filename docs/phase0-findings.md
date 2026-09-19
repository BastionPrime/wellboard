# OPE-2271 — Фаза 0: разведка nikki и Remnawave (проверенные факты)

Дата: 2026-09-19. Исполнитель разведки: adm-dev-lead (клонирование и чтение исходников — лично,
т.к. назначение adm-dev-eng ещё не создано; работа не кодовая, границы роли не нарушены).
Источники: git clone --depth 1 https://github.com/nikkinikki-org/OpenWrt-nikki.git (main, pushed 2026-09-18),
git clone --depth 1 https://github.com/remnawave/backend.git. Копии исходников: `src/nikki/`, `src/remnawave/`.

## A. nikki — проверенные факты (снимает «⚠ проверить» из ТЗ 5.5)

Файл-источник ниже — путь внутри клона nikki.

1. **Пути (include.sh:4-10):** `HOME_DIR=/etc/nikki`, `PROFILES_DIR=/etc/nikki/profiles`,
   `SUBSCRIPTIONS_DIR=/etc/nikki/subscriptions`, `RUN_DIR=/etc/nikki/run`,
   `RUN_PROFILE_PATH=/etc/nikki/run/config.yaml`, `PROVIDERS_DIR=/etc/nikki/run/providers`.
   → ТЗ 5.5 гипотеза подтверждена.
2. **Активный профиль (nikki.conf, секция `config`):** `option 'profile' '<type>:<id>'`, где
   type = `file` | `subscription`; для file — `profile:file:<имя_файла>` (nikki.init:117-127:
   `profile_file="$PROFILES_DIR/$profile_name"`, затем `cp -f → $RUN_PROFILE_PATH`).
   → WellBoard пишет профиль как файл в `/etc/nikki/profiles/` и активирует через
   `uci set nikki.config.profile='file:<name>'; uci commit nikki`.
3. **Mixin:** UCI-секция `nikki.mixin` задаёт поверх профиля всё транспортное (nikki.conf: ports,
   tun, dns, api_listen) + файл `/etc/nikki/mixin.yaml` (mixin.yaml: «You can set any mihomo
   profile's config at here, it will mixin to the profile», ниже приоритета UCI-опций LuCI).
   → ТЗ 5.4 п.5 верен: генерировать ТОЛЬКО `proxies/proxy-groups/rule-providers/rules`,
   транспорт nikki сам добавит mixin'ом; `nikki-rules`/`nikki-proxies` в mixin.yaml — prepend.
4. **API mihomo (ucode/mixin.uc:32,38):** `external-controller = uci nikki.mixin.api_listen`
   (по умолчанию `[::]:9090`), `secret = uci nikki.mixin.api_secret` (генерится при установке
   uci-defaults/init.sh: `random=$(awk ... %06d)` 6 цифр). → WellBoard читает адрес/секрет из UCI,
   не переопределяет. secret — 6-значное число, НЕ криптостойкое; в DECISIONS отметить риск.
5. **Сервис:** `/etc/init.d/nikki` (procd, START=99), команды start/stop/restart/status +
   `update_subscription <section>`, `clear_logs`. Бинарник ядра `/usr/bin/mihomo`
   (PROG в nikki.init:7).
6. **UI**: uci `nikki.mixin.ui_path` (по умолчанию `ui`), `ui_url` — zashboard CDN-dist. Каталог
   UI относительно RUN_DIR: `/etc/nikki/run/ui`. → можно переиспользовать для metacubexd или
   ставить свой (FR-8.2, решение — в Фазе 5, Vendoring vs download).
7. **Валидация:** в nikki нет отдельного «validate»; WellBoard вызывает `mihomo -t -f <path>`
   сам (ТЗ FR-6.2 остаётся в силе).

## B. Remnawave backend — проверенные факты (снимает «⚠ проверить» из ТЗ 5.7)

1. **Формат выдачи зависит от subscription-response-rules (админских), а не от UA-подстроки.**
   Middleware `src/modules/subscription-response-rules/middleware/response-rules.middleware.ts:39-68`:
   правила матчатся по request headers (в т.ч. `User-Agent`) операторами
   EQUALS/NOT_EQUALS/CONTAINS/NOT_CONTAINS/STARTS_WITH/ENDS_WITH/REGEX и др. (matcher
   services/response-rules-matcher.service.ts:84-108). ВНИМАНИЕ: **у панели НЕТ дефолтного
   правила «UA содержит mihomo → отдать mihomo»** — правила задаёт админ панели. При
   отсутствии матча — HTTP 403 (ERRORS.FORBIDDEN).
2. **Гарантированный способ получить mihomo-конфиг — path-based clientType** (middleware:53-56 →
   matcher:173-199): путь `/api/sub/<uuid>/<clientType>` (REQUEST_TEMPLATE_TYPE:
   `stash|singbox|mihomo|json|v2ray-json|clash`). clientType=mihomo → responseType MIHOMO
   (render-templates.service.ts:64 → mihomo.generator.service.ts). Если админ включил
   `disableSubscriptionAccessByPath` — путь заблокирован (BLOCK→403).
3. **UA-подстрока mihomo НЕ входит ни в EXTENDED_CLIENTS_REGEXES, ни в
   JSON_SUBSCRIPTION_FALLBACK_CLIENTS** (subscription-template/constants/extended-clients.ts:1-28).
   Happ входит в оба списка (расширенные клиенты → HTML-страницы подстановок в шаблонах). Наш UA
   `WellBoard/<ver> mihomo` на Remnawave без спец-правил может не сматчиться. → Фаза 2 должна:
   (а) пробовать `<base>/mihomo` если URL без сегмента, (б) как fallback отправлять UA c
   `mihomo`-подстрокой на случай кастомных правил CONTAINS, (в) фиксировать в DECISIONS.
4. **HWID-заголовки (common/utils/extract-hwid-headers/extract-hwid-headers.util.ts):** панель
   читает `x-hwid` (валидация `^[a-zA-Z0-9=-]{10,64}$`), `x-device-os`, `x-ver-os`,
   `x-device-model`, `user-agent`. → ТЗ FR-2.3 точно подтверждено.
5. **Ответные заголовки (subscription.service.ts:199-241, 360-378):** `x-hwid-active: true`,
   `x-hwid-not-supported: true`, `x-hwid-max-devices-reached: true`, `x-hwid-limit: true`.
   → ТЗ FR-2.4 подтверждено дословно.
6. **Ответ как на 404 может быть статус-правилом STATUS_CODE_404** (response-rules) — не только
   «подписка не найдена». Обрабатывать вместе с x-hwid-заголовками.
7. **`subscription-userinfo`/`announce`:** панель отдаёт `subscription-userinfo` (upload/download/
   total/expire — см. prisma-модель/сервис) и `announce` — соответствует ТЗ FR-7; тест-функциональность
   подтвердить в Фазе 2 на mock-сервере по этим заголовкам.

## C. Расхождение с ТЗ (нужно решение владельца, не блокирует Фазу 0)

| # | Тезис ТЗ | Проверено | Действие |
|---|---|---|---|
| C1 | «Remnawave выбирает формат по подстроке UA mihomo» (5.7.1) | **Опровергнуто**: формат выбирают admin response-rules по правилам на заголовки; гарантия только через path clientType | Внести в DECISIONS; запросить у заказчика тест-ссылку его панели, чтобы увидеть её правила (Фаза 2) |

## D. Что дальше по роли (lead)

- Создать дочерний тикет «OPE-2271 Фаза 0: каркас» (assignee adm-dev-eng): Go-модуль, `make build`,
  `/api/v1/health`, LICENSE MIT, README, CI, DECISIONS.md с фактами из A/B выше.
- Далее фазы 1-7 по ТЗ. Одна фаза = один PR в central repo.
- Окружение сборки: хост без Go; использовать docker `golang:1.23-alpine` (образ уже скачан),
  node v20.19.2 на хосте; тикет на bootstrap /srv/git у adm (прав нет) — пока git-remote в /srv/dev/git.
