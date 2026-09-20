# Установка WellBoard на OpenWrt

WellBoard — менеджер подписки Remnawave для nikki/mihomo: управляет
серверами, квотами и HWID, строит маршруты по шаблонам, генерирует
профиль nikki и применяет его с проверкой здоровья и автооткатом.
Веб-интерфейс поднимается на порту **8090**.

## Содержание
- [Требования](#требования)
- [Установка .ipk (opkg)](#установка-ipk-opkg)
- [Установка .apk (apk, OpenWrt 24.10+)](#установка-apk-apk-openwrt-2410)
- [Первый запуск](#первый-запуск)
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
| ~20 МБ RAM / ~30 МБ flash | Go-бинарь ~15 МБ + state-каталог |

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

## Установка .apk (OpenWrt 24.10+)

OpenWrt 24.10 перешёл на apk; пакеты формата `.apk` ставятся так:

```sh
apk update
apk add nikki mihomo        # из стороннего репозитория nikki
scp wellboard-*-arm_cortex-v7.apk root@192.168.1.1:/tmp/
apk add --allow-untrusted /tmp/wellboard-*-arm_cortex-v7.apk
```

`--allow-untrusted` нужен для локального неподписанного пакета; при
подключённом репозитории WellBoard подпись проверяется штатно.

## Первый запуск

Сервис стартует автоматически при установке (`enabled '1'` в
`/etc/config/wellboard` + uci-defaults вызывает
`/etc/init.d/wellboard enable`).

1. Откройте `http://<ip-роутера>:8090/` — запустится мастер первого
   запуска: подписка → политика по умолчанию → первый шаблон.
2. Вставьте ссылку подписки Remnawave, следуйте шагам мастера.
3. Нажмите **Применить** — WellBoard проверит конфиг (`mihomo -t`),
   активирует профиль nikki и проконтролирует здоровье.
4. Кнопка мониторинга открывает MetaCubeXD (нужен запуск
   `scripts/fetch-metacubexd.sh` — см. README).

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
    # пусто ('') — все интерфейсы, включая WAN: НЕ рекомендуется
    # без firewall-правил (аутентификации в v1 нет).
    #option bind 'br-lan'
    option state_dir '/etc/wellboard'
    option templates_dir '/usr/share/wellboard/templates'
```

Изменения применяются после `/etc/init.d/wellboard restart`.
Секрет API mihomo не хранится в этом конфиге — WellBoard читает
`nikki.mixin.api_secret` в рантайме.

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

См. также `docs/DECISIONS.md` (архитектурные решения) и README.
