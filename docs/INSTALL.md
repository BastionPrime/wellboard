# Установка WellBoard на OpenWrt

WellBoard — менеджер подписки Remnawave для nikki/mihomo: управляет
серверами, квотами и HWID, строит маршруты по шаблонам, генерирует
профиль nikki и применяет его с проверкой здоровья и автооткатом.
Веб-интерфейс поднимается на порту **8090**.

## Содержание
- [Требования](#требования)
- [Установка .ipk (opkg)](#установка-ipk-opkg)
- [Установка .apk (apk-tools, OpenWrt 24.10 и 25.12)](#установка-apk-apk-tools-openwrt-2410-и-2512)
- [Первый запуск и вход](#первый-запуск-и-вход)
- [Автозапуск](#автозапуск)
- [Настройка (UCI)](#настройка-uci)
- [Сохранность данных при sysupgrade](#сохранность-данных-при-sysupgrade)
- [Обновление и удаление](#обновление-и-удаление)
- [Диагностика](#диагностика)

## Требования

| Зависимость | Назначение |
|---|---|
| `nikki` | прокси-стек: mihomo, UCI-интеграция, procd-сервис |
| `ca-bundle` | TLS для запросов подписки |
| `curl` / `wget` | загрузка геоданных и подписок |
| `ubus` / `rpcd` | проверка пароля root при входе в интерфейс (входят в base OpenWrt) |
| ~15 МБ RAM / ~15 МБ flash | Go-бинарь ~9 МБ (с -s -w, v1.0.4) + state-каталог |

Пакет собран для архитектур: `aarch64_cortex-a53`,
`aarch64_generic`, `x86_64`, `arm_cortex-a7_neon-vfpv4`, `mipsel_24kc`.

Проверить архитектуру роутера: `opkg print-architecture` (или
`ubus call system board | jsonfilter -e '@.architecture'`).

## Установка .ipk (opkg)

```sh
# 1) обновить индексы, поставить зависимости из репозитория nikki
opkg update
opkg install nikki mihomo   # если ещё не стоят

# 2) поставить WellBoard (файл на роутере, например через scp)
scp wellboard_*_arm_cortex-a7.ipk root@192.168.1.1:/tmp/
opkg install /tmp/wellboard_*_arm_cortex-a7.ipk

# 3) убедиться, что сервис запущен
/etc/init.d/wellboard status
```

opkg сам подтянет `ca-bundle` и `curl`; `nikki` должен быть
установлен заранее из [репозитория nikki](https://github.com/nikkinikki-org/OpenWrt-nikki)
(см. его README) — он не входит в стандартные репозитории OpenWrt.

## Установка .apk (apk-tools, OpenWrt 24.10 и 25.12)

> **Формат пакета (обновлено).** Начиная с 1.0.4 `.apk` собирается
> apk-tools 3 `apk mkpkg` — формат apk-v3 (ADB-контейнер), тот же, что
> использует buildroot OpenWrt 25.12. Проверено структурно
> (`apk adbdump`, apk-tools 3.0.8). История: пакеты до 1.0.4 собирались
> вручную в layout apk-v2 и apk-tools 3 их отвергал
> (`v2 package format error`). Контракты службы (UCI/procd/
> uci-defaults/keep.d) от формата не зависят; на 23.05/24.10 с opkg
> путь ниже работает (.ipk).

OpenWrt 24.10 перешёл с opkg на apk; в 25.12 apk — штатный менеджер
пакетов, `opkg` в системе нет. Порядок установки `.apk`:

```sh
# 0) версия и менеджер пакетов
cat /etc/openwrt_release            # VERSION / DISTRIB_RELEASE (например, 25.12.0)
apk --version                       # apk-tools 3.x

# 1) зависимости
apk update
apk add nikki mihomo                # из стороннего репозитория nikki

# 2) сам пакет (файл на роутере, например через scp)
scp wellboard-<arch>.apk root@192.168.1.1:/tmp/
apk add --allow-untrusted /tmp/wellboard-<arch>.apk

# 3) состав установки и запуск
apk info -L wellboard | head
/etc/init.d/wellboard status
```

`--allow-untrusted` нужен для локального неподписанного пакета; при
подключённом репозитории WellBoard подпись проверяется штатно.

**Контракты, на которые опирается пакет** (от версии не зависят):
`/etc/config/wellboard` (UCI), `/etc/init.d/wellboard` (procd,
`START=99`), `/etc/uci-defaults/` (первичная настройка),
`/lib/upgrade/keep.d/wellboard` (сохранение `state_dir` при
sysupgrade). Проверить пакет без роутера можно в rootfs целевой ветки (тот же
набор команд используется при приёмке):

```sh
docker run --rm -v "$PWD/dist:/pkg:ro" openwrt/rootfs:x86_64-25.12-SNAPSHOT \
  sh -c 'apk add --allow-untrusted /pkg/wellboard-*.apk'
# после успешной установки:
#   sh /etc/uci-defaults/99_wellboard
#   /etc/init.d/wellboard enable && /etc/init.d/wellboard start
#   wget -q -O- http://127.0.0.1:8090/api/v1/health
```

Вход проверяется в том же rootfs: поднять `ubusd` и `rpcd`, задать
пароль root, затем `POST /api/v1/auth/login` (пример в `docs/API.md`).

## Первый запуск и вход

Сервис стартует автоматически при установке (`enabled '1'` в
`/etc/config/wellboard` + uci-defaults вызывает
`/etc/init.d/wellboard enable`).

1. Откройте `http://<ip-роутера>:8090/`. Появится экран входа —
   введите **пароль root роутера** (тот же, что для LuCI). Пароль
   проверяется через ubus и нигде не сохраняется; своя учётная запись
   не заводится. Если вход отключён (`option auth '0'`), панель
   откроется сразу.
2. Панель открывается на главном экране. Мастер первого запуска
   автоматически не всплывает — он вызывается кнопкой **«Запустить
   мастер настройки»** в Настройках (подписка → политика по
   умолчанию → первый шаблон).
3. Существующие правила из nikki можно посмотреть, не меняя конфиг:
   вкладка **Маршруты → Внешние правила nikki** (только чтение) или
   кнопка импорта подписки в Настройках. Конфиг `/etc/nikki`
   WellBoard не изменяет до нажатия **Применить**.
4. Нажмите **Применить** — WellBoard проверит конфиг (`mihomo -t`),
   активирует профиль nikki и проконтролирует здоровье.

Сессия живёт 12 часов; перезапуск службы требует повторного входа.
Выход — кнопка **Выйти** в шапке.

Проверка живости из консоли:

```sh
wget -q -O- http://127.0.0.1:8090/api/v1/health
# {"status":"ok","version":"..."}
```

## Автозапуск

Сервис зарегистрирован в procd со `START=99`:

```sh
/etc/init.d/wellboard enable    # автозапуск при загрузке
/etc/init.d/wellboard start     # запустить сейчас
/etc/init.d/wellboard stop      # остановить
/etc/init.d/wellboard restart   # перезапустить
/etc/init.d/wellboard status    # health-check /api/v1/health
```

В LuCI пункт меню **Services → WellBoard** перенаправляет на
веб-интерфейс (порт из UCI).

## Настройка (UCI)

`/etc/config/wellboard`:

```conf
config wellboard 'main'
    option enabled '1'
    option port '8090'
    # Адрес прослушивания (NFR-2.1): unset — только LAN (br-lan, рекомендуется);
    # IP ('192.168.1.1') или имя интерфейса ('br-lan') — явно;
    # пусто ('') — все интерфейсы, включая WAN: НЕ рекомендуется,
    # даже со включённым входом.
    #option bind 'br-lan'
    option state_dir '/etc/wellboard'
    option templates_dir '/usr/share/wellboard/templates'
    # Вход по паролю root роутера (проверка через ubus) + CSRF.
    # 1 — включён (по умолчанию), 0 — выключен (только доверенная сеть).
    option auth '1'
```

Изменения применяются после `/etc/init.d/wellboard restart`.
Секрет API mihomo не хранится в этом конфиге — WellBoard читает
`nikki.mixin.api_secret` в рантайме. Флаг `--no-auth` у бинарника и
переменная `WELLBOARD_AUTH` делают то же, что `option auth`.

## Сохранность данных при sysupgrade

HWID и всё состояние (подписки, серверы, маршруты, сгенерированные
профили, логи) лежат в `/etc/wellboard`. Каталог внесён в
`/lib/upgrade/keep.d/wellboard`, поэтому `sysupgrade` сохраняет его:

```sh
cat /lib/upgrade/keep.d/wellboard   # содержит /etc/wellboard
sysupgrade -v /tmp/openwrt-…-squashfs-sysupgrade.bin
```

Флаги `-n` / `-l` у sysupgrade отключают сохранение — не используйте
их, если нужны данные WellBoard.

## Обновление и удаление

```sh
opkg install /tmp/wellboard_*_new.ipk     # обновление
opkg remove wellboard                     # удаление (state в /etc/wellboard остаётся)
opkg remove luci-app-wellboard            # убрать пункт LuCI
rm -rf /etc/wellboard                     # удалить данные полностью
```

## Диагностика

```sh
logread | grep wellboard          # syslog
cat /var/log/wellboard.log        # собственный лог WellBoard
/etc/init.d/wellboard status      # ответ /api/v1/health
curl http://127.0.0.1:8090/api/v1/diagnostics   # точечные проверки
```

Вход и ubus:

```sh
logread | grep 'auth:'            # "auth: enabled, ubus login as "root"…"
ubus call session login '{"username":"root","password":"…"}'   # проверка ubus
```

- `503` при входе — ubus не отвечает (служба `rpcd`/`ubus` не
  запущена), это не «неверный пароль».
- `401` — пароль не подошёл; `429` — 5 неудач с одного адреса за
  минуту, подождите минуту.

Полный перечень HTTP-маршрутов — в `docs/API.md`.

См. также `docs/DECISIONS.md` (архитектурные решения) и README.
