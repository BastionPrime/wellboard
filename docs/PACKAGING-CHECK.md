# OpenWrt packaging desk-check (OPE-3749)

Построчная сверка `packaging/openwrt/` (Makefile, files/uci-defaults.sh,
files/wellboard.init, files/wellboard.config, files/keep.d/wellboard)
против OpenWrt-конвенций. Проверено против v1.0.4 (main 3892350).
Скрипты сборки (`scripts/package-ipk.sh`, `package-apk.sh`) сверены
параллельно — они ставят тот же набор файлов (DECISIONS-phase6 D25).

## 1. Makefile (`packaging/openwrt/Makefile`)

| Строка | Что проверено | Вердикт |
| --- | --- | --- |
| 8 | `PREBUILT_DIR ?= $(CURDIR)/prebuilt` | OK (см. замечание P3: каталог никто не наполняет) |
| 13 | `PKG_VERSION:=1.0.4` = `cat VERSION` | OK, совпадает (дублирование — P4) |
| 15–16 | License/Maintainer | OK; совпадает с `scripts/package-ipk.sh` control |
| 26 | `DEPENDS:=+ca-bundle +curl +nikki` | OK; = `DEPENDS="ca-bundle, curl, nikki"` в package-ipk.sh; nikki — вне официальных фидов (P7) |
| 36–38 | `conffiles /etc/config/wellboard` | OK: конфиг переживает upgrade, не попадает в .new-конфликт |
| 40–47 | `Build/Prepare`: mkdir + громкая проверка наличия бинарника | OK: без prebuilt сборка падает с понятной ошибкой |
| 49–51 | `Build/Compile` = no-op | OK (D23: бинарник прекомпилирован) |
| 54–55 | `$(INSTALL_BIN)` → `/usr/bin/wellboard` 0755 | OK; = package-ipk.sh (`install -m 0755`) |
| 57–60 | templates: `$(CP)` + `find -type d → 0755`, `-type f → 0644` | OK; идентично package-ipk.sh (те же find-строки) |
| 62–63 | `/etc/wellboard` через `$(INSTALL_DIR)` + `chmod 0700` | OK: состояние (HWID и пр.) закрыто от остальных |
| 65–66 | `$(INSTALL_CONF)` → `/etc/config/wellboard` | Рабочий, но режим отличается от скрипта (P5) |
| 68–69 | `/etc/init.d/wellboard` `$(INSTALL_BIN)` 0755 | OK |
| 71–72 | `/etc/uci-defaults/99_wellboard` `$(INSTALL_BIN)` 0755 | OK; = package-ipk.sh |
| 74–75 | `/lib/upgrade/keep.d/wellboard` `$(INSTALL_DATA)` 0644 | OK; = package-ipk.sh |
| 78 | `$(eval $(call BuildPackage,wellboard))` | OK |

## 2. files/uci-defaults.sh

| Строка | Что проверено | Вердикт |
| --- | --- | --- |
| 6–9 | `mkdir -p /etc/wellboard; chmod 0700` | OK: идемпотентно, права совпадают с Makefile |
| 13–21 | `uci -q get` guard → set main/port/state_dir/templates_dir/auth → `uci commit` | OK: выполняется только если секции нет; не затирает изменённый пользователем конфиг; exit-код `uci -q get` при отсутствии = не 0, guard корректен |
| 25–27 | `IPKG_INSTROOT` guard вокруг `enable` | OK: в chroot-установке симлинки rc.d не создаются |
| 29 | `exit 0` | OK: обязательно, иначе скрипт не удалится и повторится на каждой загрузке |

Замечание P6 (косметика, исправлено в этом PR): комментарий «opkg
deletes /etc/uci-defaults entries» неточен — скрипты запускает и удаляет
`/etc/init.d/boot` на первой загрузке, opkg их не исполняет.

## 3. files/wellboard.init

| Строка | Что проверено | Вердикт |
| --- | --- | --- |
| 10–11 | `START=99 STOP=10` | OK: зеркало nikki; стартует последним (сеть/ubus уже подняты), останавливается одним из первых |
| 12 | `USE_PROCD=1` | OK |
| 26 | `export PATH=...` | OK: procd даёт минимальный PATH |
| 31–36 | `config_load` + `config_get_bool enabled` → gate с logger | OK: `enabled=0` не мешает boot, пишет в лог |
| 37 | `config_get port main port` → `WELLBOARD_PORT` | OK: единственная ручка порта у бинарника (D28) |
| 41–45 | bind: unset → без флага; значение → `--bind <val>`; **пусто → сломано** | **P1 — баг, см. список проблем** |
| 52–53 | `mkdir -p $state_dir; chmod 0700` | OK: идемпотентно, права как у пакета |
| 57–66 | procd: command/pidfile/stdout/stderr/`file /etc/config/wellboard`/respawn | OK: перезапуск при изменении конфига; `respawn_retry=0` — конвенция procd «без лимита» |
| 69–74 | `reload_service` = stop+start | OK: у Go-бинарника нет SIGHUP-обработчика (D28) |
| 76–83 | `status()` через busybox wget на 127.0.0.1:$port | OK: busybox wget есть в базовом образе |
| 85–87 | `run_triggers` заглушка | OK (намеренно, как у nikki) |

## 4. files/wellboard.config

| Строка | Вердикт |
| --- | --- |
| 4 `config wellboard 'main'` | OK: тип+имя секции совпадают с чтением в init (`config_load wellboard`, `config_get … main …`) и с uci-defaults |
| 6 `option enabled '1'` | OK: bool, читается `config_get_bool` |
| 8 `option port '8090'` | OK: = DEFAULT_PORT, = uci-defaults, = документация (INSTALL.md) |
| 14 закомментированный `bind` | OK: unset → LAN-only у бинарника (NFR-2.1); но см. P1 про пустое значение |
| 17 `state_dir '/etc/wellboard'` | OK: существует в keep.d — переживает sysupgrade |
| 19 `templates_dir '/usr/share/wellboard/templates'` | OK: путь установки из Makefile/скриптов; main.go:158 использует тот же путь по умолчанию в prod |
| 23 `auth '1'` | OK: читается `config_get_bool`, проксируется в `WELLBOARD_AUTH` |

## 5. files/keep.d/wellboard

- Содержимое: единственная строка `/etc/wellboard` — OK: весь стейт
  (HWID, подписки, профили, логи) переживает sysupgrade; паттерн как у
  nikki (D27). Формат: одна запись на строку, без glob — корректно.
- `/etc/config/wellboard` в keep.d не нужен: он сохраняется через
  `conffiles` в Makefile (uci-механизм), двойное сохранение не требуется.
- Реальный sysupgrade в контейнере непроверяем (D27: отложено на
  BPI-R4 заказчика) — принято как есть.

## 6. Порядок старта / остановки

- `START=99`: wellboard стартует после network + nikki — достаточно,
  сам делает health-check и rollback при проблемах профиля.
- `STOP=10`: останавливается раньше nikki — обратный порядок корректен.
- `uci-defaults` (первая загрузка) вызывает `/etc/init.d/wellboard enable`
  вне `IPKG_INSTROOT` — симлинки S99/K10 создаются, автостарт включён.
- `procd_set_param file /etc/config/wellboard` перезапускает сервис при
  `uci commit wellboard` — согласовано с reload=stop+start.

## 7. Права и пути (сводка)

| Путь | Права | Источник |
| --- | --- | --- |
| `/usr/bin/wellboard` | 0755 | Makefile L55, package-ipk.sh, package-apk.sh |
| `/usr/share/wellboard/templates/` | d0755 / f0644 | Makefile L59–60, оба скрипта |
| `/etc/wellboard` | 0700 | Makefile L63, uci-defaults L8, init L53 |
| `/etc/config/wellboard` | 0600 (buildroot) / 0644 (ipk/apk) | P5 — рассинхрон |
| `/etc/init.d/wellboard` | 0755 | везде одинаково |
| `/etc/uci-defaults/99_wellboard` | 0755 | везде одинаково |
| `/lib/upgrade/keep.d/wellboard` | 0644 | везде одинаково |

## 8. Найденные проблемы (классификация)

1. **Блокер (edge case, документированное поведение сломано)** —
   `files/wellboard.init` L41–45: при `option bind ''` (пустое значение,
   задокументированное в wellboard.config как «все интерфейсы»)
   `bind_flag="--bind "` из-за незакавыченного `$bind_flag` в
   `procd_set_param command` разбивается на слова и пустой аргумент
   теряется: бинарник получает `--bind` без значения → ошибка Go flag
   («flag needs an argument»), сервис не стартует. Пользователь,
   который «откроет все интерфейсы» по документации, получает
   неработающий сервис. Исправление (не вошло в этот PR, вне класса
   «мелкие правки»): ветвление — при установленном пустом bind
   передавать `--bind ""` отдельным закавыченным аргументом.
2. **Среднее** — `scripts/cross-compile.sh` собирает без
   `-X main.version=1.0.4` (только `-s -w`): все бинарники в пакетах
   сообщают `version: "dev"` (D24 это подтверждает), тогда как пакет
   заявлен 1.0.4, а комментарий в packaging/openwrt/Makefile L11–12
   утверждает обратное. Диагностика/поддержка получат неверную версию.
3. **Среднее (документация/интеграция buildroot)** — комментарии
   Makefile ссылаются на `make cross` как источник
   `prebuilt/wellboard`, но cross-compile.sh пишет в `.build/wellboard-<arch>`
   и ничего не кладёт в `packaging/openwrt/prebuilt/`; `$(CURDIR)/templates`
   внутри каталога пакета тоже не существует. Buildroot-путь как
   написано не воспроизводим без ручного стейджинга (релизный путь —
   scripts/package-*.sh, D25). Комментарий поправлен в этом PR.
4. **Косметика** — `PKG_VERSION` дублируется в трёх местах (VERSION,
   packaging/openwrt/Makefile, luci-app Makefile) с комментарием «the
   release process updates both», но автоматики нет — рассинхрон
   возможен вручную.
5. **Косметика** — режим `/etc/config/wellboard`: buildroot-путь
   ставит 0600 (`INSTALL_CONF`), ipk/apk-скрипты 0644. На поведение не
   влияет, но пути расходятся.
6. **Косметика (исправлено в этом PR)** — комментарий uci-defaults
   про «opkg deletes» заменён на корректный механизм
   (/etc/init.d/boot на первой загрузке).
7. **Косметика** — `DEPENDS +nikki`: пакета нет в официальных фидах
   OpenWrt, установка требует кастомный фид (в данной среде это
   ожидаемо — nikki и есть целевой рантайм).

## 9. make build

PR не трогает ни одного входа сборки Go (только новый docs/-файл и
комментарии в shell-скриптах), поэтому `make build` (docker
golang:1.23-alpine) не меняется. Синтаксис изменённых shell-файлов
проверен `sh -n` (OK). Docker на этой ВМ отсутствует — запуск
`make build` из этого окружения невозможен (см. тикет).
