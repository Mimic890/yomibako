# Yomibako — runtime proxy для BookOrbit

Плагины Mihon и LNReader без переписывания парсеров: тонкие BookOrbit-плагины ходят в один
локальный сервис, а он поднимает нужный рантайм только на время запроса и гасит его после
простоя.

```
BookOrbit
  ├── indexer mihon-runtime    ─┐
  └── indexer lnreader-runtime ─┤
                                ▼
                    Yomibako (этот репозиторий)
                        ├── miwayomi (Docker, on-demand)
                        └── lnreader CLI (процесс на запрос)
                                │
                                ▼
                       CBZ / EPUB → BookOrbit
```

Yomibako — имя сервиса; `runtime proxy` осталось только описанием его роли.

## Что внутри

| Путь | Что это |
|---|---|
| `proxy/server.mjs` | Node-версия HTTP-сервиса (оставлена как альтернатива) |
| `proxy/runtimes/mihon.mjs` | старт/стоп контейнера miwayomi, поиск, сборка CBZ |
| `proxy/runtimes/lnreader.mjs` | вызов `@lnreader/cli` на поиск и скачивание EPUB |
| `proxy/zip.mjs` | потоковый ZIP без зависимостей (CBZ) |
| `proxy-go/` | Yomibako на Go 1.27.4: HTTP-сервис, рантаймы, CBZ через `archive/zip` |
| `plugins/mihon/index.mjs` | BookOrbit indexer plugin для Mihon-источников |
| `plugins/lnreader/index.mjs` | BookOrbit indexer plugin для LNReader-источников |
| `compose.yml` | контейнер miwayomi с лимитом памяти |

У Yomibako внешних зависимостей нет: только Go 1.27.4. Старая Node-версия требует Node.js 22+.

## Быстрый старт

1. **Mihon-рантайм.** Создать контейнер (один раз):

   ```bash
   docker compose up -d
   ```

   Yomibako сам останавливает и запускает `miwayomi`, поэтому `restart: "no"`.
   Дальше в WebUI miwayomi (`http://127.0.0.1:4567`) добавить репозиторий Keiyoushi и
   установить нужные расширения, например MangaLib и HentaiLib.

2. **LNReader-рантайм.** Поставить CLI:

   ```bash
   npm i -g @lnreader/cli
   lnreader plugins list            # проверить, что RLIB на месте
   lnreader auth RLIB               # только если источнику нужен вход
   ```

   Id плагина не всегда совпадает с названием сайта: у RanobeLib это `RLIB`, и CLI
   чувствителен к регистру.

   Если источнику нужен вход, делайте `auth` с тем же `--home`, что указан в конфиге Yomibako,
   иначе cookie окажутся в другом профиле.

3. **Yomibako.**

   Go-версия (основная):

   ```bash
   cd proxy-go
   cp config.example.json config.json
   go run .
   ```

   Старая Node-версия (альтернатива):

   ```bash
   cp proxy/config.example.json proxy/config.json
   node proxy/server.mjs
   ```

   Проверка: `curl http://127.0.0.1:7345/health`.

4. **Плагины BookOrbit.** Скопировать каталоги:

   ```
   <APP_DATA_PATH>/plugins/indexers/mihon-runtime/index.mjs
   <APP_DATA_PATH>/plugins/indexers/lnreader-runtime/index.mjs
   ```

   Перезапустить BookOrbit, затем в **Settings → Server → Requests → Sources** добавить
   источник для каждого плагина:

   - Base URL: `http://127.0.0.1:7345` (или адрес, доступный из контейнера BookOrbit);
   - в настройках источника указать имя источника miwayomi (`MangaLib`) или id LNReader-плагина
     (`RLIB`);
   - включить **Allow private addresses**, иначе BookOrbit не пустит запрос на localhost.

   На каждый источник — отдельная запись indexer: плагин ищет ровно в одном источнике.

## API Yomibako

| Метод | Путь | Тело | Ответ |
|---|---|---|---|
| `GET` | `/health` | — | состояние рантаймов, ничего не запускает |
| `POST` | `/v1/search` | `{runtime, source, title, author, limit}` | `{releases: [...]}` |
| `POST` | `/v1/grab` | `{runtime, source, guid, title}` | `{id, fileName, format, url}` сразу, файл готовится в фоне |
| `GET` | `/v1/files/<id>` | — | CBZ/EPUB, ждёт окончания сборки |

`runtime` — `mihon` или `lnreader`. Ошибки — JSON `{error}` и HTTP 4xx/5xx.
Если в конфиге задан `token`, нужен заголовок `Authorization: Bearer <token>` (плагины берут
его из поля «Yomibako token»).

## Конфиг

`proxy-go/config.json` (или `proxy/config.json` у Node-версии; все поля необязательны, значения по
умолчанию — в `proxy-go/config.example.json`):

| Поле | Смысл |
|---|---|
| `host`, `port` | адрес Yomibako; по умолчанию только localhost |
| `token` | общий токен; пусто — без авторизации |
| `fileTtlMinutes` | сколько хранить готовые файлы |
| `mihon.container` | контейнер, который стартовать/стопать; пусто — не управлять |
| `mihon.baseUrl` | API miwayomi/Suwayomi |
| `mihon.startTimeoutMs` | сколько ждать health после старта JVM |
| `mihon.idleMinutes` | простой до `docker stop` |
| `mihon.imageConcurrency` | параллельных загрузок страниц |
| `mihon.maxChapters` | `0` — весь тайтл; число — ограничить главы |
| `lnreader.command`, `home` | бинарь CLI и его профиль (`--home`) |

Переменные окружения Yomibako: `YOMIBAKO_HOST`, `YOMIBAKO_PORT`, `YOMIBAKO_TOKEN`,
`MIWAYOMI_URL`, `YOMIBAKO_CONFIG`. Старая Node-версия по-прежнему читает `RUNTIME_PROXY_*`.

## Проверка

```bash
(cd proxy-go && go test ./...)   # Yomibako: ZIP, CBZ-сборка, jobs; без сети и Docker
node proxy/selfcheck.mjs         # старая Node-версия: ZIP/CRC, имена и контракт плагинов
docker compose config            # синтаксис compose
```

## Ограничения и риски

- **BookOrbit Plugin API v1 больше не запрашивает комиксы**, только ebook/audiobook. Плагин
  Mihon отдаёт CBZ с `mediaKinds: ['ebook']` и `format: 'cbz'`; если BookOrbit отфильтрует
  формат — это первое место, которое надо смотреть.
- **Скачивается весь тайтл целиком.** У манги это сотни глав и гигабайты; `maxChapters`
  ограничивает, но полноценного «выбрать главы» в indexer-контракте нет.
- **Первый запрос после простоя дольше**: JVM miwayomi стартует десятки секунд. У BookOrbit
  есть свой дедлайн на запрос; при таймауте поиск надо повторить.
- **Страницы некоторых источников требуют заголовков/Cloudflare.** Картинки идут через
  `/api/v1/proxy` miwayomi, но если источник без FlareSolverr не отвечает — нужен FlareSolverr
  (поле `FLARESOLVERR_URL` в compose).
- **LNReader CLI — prerelease.** Флаги и JSON-вывод могут поменяться; ломается в одном месте —
  `proxy/runtimes/lnreader.mjs`.
- **Прокси может запускать контейнеры и скачивать файлы**, поэтому по умолчанию слушает только
  localhost. Наружу — только с `token` и за обратным прокси.
- Плагины и рантаймы запускают чужой код расширений. Ответственность за источники и
  использование контента — на владельце инстанса.
