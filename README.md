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

Yomibako — имя сервиса, а «runtime proxy» — описание его роли: для BookOrbit это один
HTTP-сервис на `127.0.0.1:7345`, внутри которого живут два рантайма, выключенные, пока к ним не
обратятся.

## Установка

Три шага: поднять сервис, положить плагины в BookOrbit, добавить источники. Всё остальное —
ниже.

### 1. Запустить сервис

```bash
cp .env.example .env          # задать YOMIBAKO_TOKEN и DOCKER_GID
docker compose up -d          # тянет ghcr.io/mimic890/yomibako:latest и поднимает miwayomi
curl -s http://127.0.0.1:7345/health
```

`DOCKER_GID` — GID владельца `/var/run/docker.sock` (`stat -c '%g' /var/run/docker.sock`), без
него Yomibako не сможет запускать miwayomi. `YOMIBAKO_TOKEN` можно оставить пустым, пока сервис
не выходит за пределы localhost.

Образ `yomibako` публикуется из этого репозитория в GHCR. Если реестр недоступен, раскомментируйте
`build:` у сервиса `yomibako` в `compose.yml` и соберите локально: `docker compose build yomibako`.
Подробности — в разделе «Деплой».

Что произойдёт:

1. `yomibako` слушает `127.0.0.1:7345` и видит Docker-сокет, чтобы управлять соседним контейнером.
2. `miwayomi` стартует один раз; после `mihon.idleMinutes` (10 минут) простоя Yomibako его
   останавливает, поэтому `restart: "no"` — Docker не должен поднимать его обратно.
3. Первый запрос после простоя снова запускает контейнер и ждёт `/api/v1/health` до
   `mihon.startTimeoutMs`.

Запускайте `up -d` без имён сервисов: контейнер `miwayomi` должен существовать, иначе Yomibako
нечего будет останавливать и запускать.

Перед добавлением источников нужен хотя бы один установленный рантайм:

- **Mihon.** Открыть WebUI miwayomi на `http://127.0.0.1:4567`, добавить репозиторий Keiyoushi и
  установить нужные расширения (например MangaLib и HentaiLib). Имя источника из WebUI потом
  указывается в настройках indexer'а. Список источников Yomibako кеширует на 5 минут и
  обновляет его при промахе, поэтому перезапуск после установки расширения не нужен.
- **LNReader.** CLI уже внутри образа:

  ```bash
  docker compose exec yomibako lnreader plugins list   # проверить, что RLIB на месте
  ```

  Id плагина не всегда совпадает с названием сайта: у RanobeLib это `RLIB`, и CLI чувствителен к
  регистру. Индексы плагинов и cookie лежат в томе `lnreader-home`
  (`/home/yomibako/.lnreader`) и переживают пересоздание контейнера.

  Если источнику нужен вход или Cloudflare, `lnreader auth` не подойдёт: он открывает настоящий
  браузер, которого в контейнере нет. Два рабочих пути:

  - экспортировать cookie из браузера в Netscape-формате (`cookies.txt`), положить файл рядом с
    `config.json` и указать `lnreader.cookies` (см. «Конфиг»);
  - либо выполнить `lnreader --home <путь-к-тому> auth RLIB` на машине с браузером, смонтировав
    туда тот же каталог, что и том `lnreader-home`.

### 2. Установить плагины в BookOrbit

Скопировать в каталог данных BookOrbit (`<APP_DATA_PATH>`, тот, где лежат его конфиг и БД):

```
<APP_DATA_PATH>/plugins/indexers/mihon-runtime/index.mjs
<APP_DATA_PATH>/plugins/indexers/lnreader-runtime/index.mjs
```

| Файл в этом репозитории | Куда | Тип indexer'а |
|---|---|---|
| `plugins/mihon/index.mjs` | `<APP_DATA_PATH>/plugins/indexers/mihon-runtime/index.mjs` | `mihon-runtime` |
| `plugins/lnreader/index.mjs` | `<APP_DATA_PATH>/plugins/indexers/lnreader-runtime/index.mjs` | `lnreader-runtime` |

Перезапустить BookOrbit: плагины читаются на старте. Оба файла — самодостаточные ES-модули без
зависимостей, ставятся копированием; каталог `proxy-go/` рядом с BookOrbit не нужен, он живёт в
контейнере.

### 3. Добавить источники в BookOrbit

BookOrbit ничего не знает про источники сам по себе: **один indexer = один источник**. Для
MangaLib и для RLIB это две отдельные записи.

**Settings → Server → Requests → Sources → Add source**, затем:

| Поле | Mihon | LNReader |
|---|---|---|
| Plugin / type | `Mihon (Yomibako)` (`mihon-runtime`) | `LNReader (Yomibako)` (`lnreader-runtime`) |
| Name | `MangaLib` (произвольно) | `RanobeLib` (произвольно) |
| Base URL | `http://127.0.0.1:7345` | `http://127.0.0.1:7345` |
| `Source name in miwayomi` | имя источника **точно как в WebUI miwayomi**, например `MangaLib` | — |
| `LNReader plugin id` | — | id из `lnreader plugins list`, например `RLIB` |
| `Yomibako token` | значение `YOMIBAKO_TOKEN`, если он задан | то же |
| Allow private addresses | **включить** | **включить** |

Пример для Mihon-источника:

```
Base URL:               http://127.0.0.1:7345
Source name in miwayomi: MangaLib
Yomibako token:          <тот же, что в .env>
Allow private addresses: on
```

Пояснения:

- **Allow private addresses** обязателен: без него BookOrbit откажется обращаться к localhost и
  к адресам из приватных диапазонов.
- **Base URL** — это адрес Yomibako с точки зрения процесса BookOrbit. Если BookOrbit запущен в
  контейнере, `127.0.0.1` — это сам контейнер: подключите BookOrbit к сети `yomibako` и укажите
  `http://yomibako:7345`, либо используйте адрес хоста (`http://host.docker.internal:7345` на
  Docker Desktop; на Linux — IP хоста в docker0). Для этого порт придётся публиковать не только на
  loopback — см. «Безопасность».
- **Source name in miwayomi** сверяется без учёта регистра с полем `name` из
  `GET http://127.0.0.1:4567/api/v1/sources`. Если имя не найдено, Yomibako вернёт список
  загруженных источников — по нему видно опечатку.
- Кнопка **Test** у источника дергает `GET /health` и проверяет, что нужный рантайм включён
  (для LNReader — что CLI доступен).
- Ошибки BookOrbit показываются как `unauthorized`, `timeout`, `unreachable` или текст `{error}`,
  который вернул Yomibako.

## Что внутри

| Путь | Что это |
|---|---|
| `proxy-go/` | Yomibako на Go 1.27: HTTP-сервис, рантаймы, CBZ через `archive/zip` |
| `proxy-go/Dockerfile` | образ Yomibako: статический бинарник + Docker CLI + `@lnreader/cli` |
| `proxy-go/config.docker.json` | конфиг внутри образа (`0.0.0.0:7345`, `http://miwayomi:4567`) |
| `compose.yml` | стек из двух сервисов: `yomibako` (готовый образ) и `miwayomi` |
| `plugins/mihon/index.mjs` | BookOrbit indexer plugin для Mihon-источников |
| `plugins/lnreader/index.mjs` | BookOrbit indexer plugin для LNReader-источников |
| `.github/workflows/ci.yml` | CI: `go test`, `docker compose config`, сборка образа |
| `.github/workflows/release.yml` | публикация образа в `ghcr.io/mimic890/yomibako` |
| `.env.example` | шаблон `.env`: `YOMIBAKO_TOKEN` и `DOCKER_GID` |
| `proxy/server.mjs` | Node-версия HTTP-сервиса (оставлена как альтернатива) |
| `proxy/runtimes/mihon.mjs` | старт/стоп контейнера miwayomi, поиск, сборка CBZ |
| `proxy/runtimes/lnreader.mjs` | вызов `@lnreader/cli` на поиск и скачивание EPUB |
| `proxy/zip.mjs` | потоковый ZIP без зависимостей (CBZ) |

У Yomibako нет внешних Go-зависимостей: только стандартная библиотека, поэтому в `proxy-go/`
нет и `go.sum` — он появится, как только добавится хотя бы один модуль (`go mod tidy`). Старая
Node-версия требует Node.js 22+.


## Локальный запуск без Docker

```bash
cd proxy-go
cp config.example.json config.json    # host 127.0.0.1, port 7345, baseUrl 127.0.0.1:4567
go run .
```

miwayomi при этом поднимается отдельно (`docker compose up -d miwayomi`), а `lnreader`
должен быть в `PATH`:

```bash
npm i -g @lnreader/cli
```

Старая Node-версия (альтернатива): `cp proxy/config.example.json proxy/config.json && node proxy/server.mjs`.

## API Yomibako

| Метод | Путь | Тело | Ответ |
|---|---|---|---|
| `GET` | `/health` | — | состояние рантаймов, ничего не запускает |
| `POST` | `/v1/search` | `{runtime, source, title, author, limit}` | `{releases: [...]}` |
| `POST` | `/v1/grab` | `{runtime, source, guid, title}` | `{id, fileName, format, url}` сразу, файл готовится в фоне |
| `GET` | `/v1/files/<id>` | — | CBZ/EPUB, ждёт окончания сборки |

`runtime` — `mihon` или `lnreader`. Ошибки — JSON `{error}` и HTTP 4xx/5xx.

Авторизация: если задан `token`, нужен `Authorization: Bearer <token>` (или `X-Proxy-Token`).
Исключение — `/health`: это liveness-проба контейнера и плагинов, она открыта всегда.
Скачивание принимает токен ещё и как `?token=<token>`, потому что BookOrbit сам забирает URL из
`/v1/grab` и не пересылает заголовки indexer'а; плагины добавляют параметр автоматически.

## Конфиг

Файл: `proxy-go/config.json` (или `proxy/config.json` у Node-версии; в контейнере —
`/etc/yomibako/config.json`). Все поля необязательны, значения по умолчанию — в
`proxy-go/config.example.json`.

| Поле | Смысл |
|---|---|
| `host`, `port` | адрес Yomibako; по умолчанию `127.0.0.1:7345`, в контейнере `0.0.0.0:7345` |
| `token` | общий токен; пусто — без авторизации |
| `fileTtlMinutes` | сколько хранить готовые файлы (по умолчанию 60) |
| `mihon.enabled` | выключить Mihon-рантайм целиком |
| `mihon.container` | контейнер, который стартовать/стопать (`miwayomi`); пусто — не управлять |
| `mihon.baseUrl` | API miwayomi; в контейнере `http://miwayomi:4567` |
| `mihon.startTimeoutMs` | сколько ждать health после старта JVM |
| `mihon.idleMinutes` | простой до `docker stop` |
| `mihon.imageConcurrency` | параллельных загрузок страниц |
| `mihon.maxChapters` | `0` — весь тайтл; число — ограничить главы |
| `lnreader.enabled` | выключить LNReader-рантайм целиком |
| `lnreader.command`, `lnreader.home` | бинарь CLI и его профиль (`--home`) |
| `lnreader.cookies` | путь к `cookies.txt` (Netscape) — вход без браузера (`--cookies`) |
| `lnreader.concurrency` | сколько процессов CLI одновременно |

Переменные окружения перекрывают файл: `YOMIBAKO_HOST`, `YOMIBAKO_PORT`, `YOMIBAKO_TOKEN`,
`MIWAYOMI_URL`, `YOMIBAKO_CONFIG` (путь к конфигу). Старая Node-версия читает `RUNTIME_PROXY_*`.

## Деплой

### Запуск

```bash
cp .env.example .env
# YOMIBAKO_TOKEN=$(openssl rand -hex 32) и DOCKER_GID=$(stat -c '%g' /var/run/docker.sock)
docker compose up -d
docker compose ps
```

`docker compose up -d` забирает готовый образ `ghcr.io/mimic890/yomibako:latest` и запускает
`miwayomi`; порт `7345` публикуется только на loopback.

### Образ Yomibako

Образ собирается и публикуется в GHCR workflow `.github/workflows/release.yml`:

| Событие | Теги |
|---|---|
| push в `main` | `latest`, `sha-<commit>` |
| тег `v1.2.3` | `1.2.3`, `1.2`, `sha-<commit>` |
| ручной запуск | те же, что для ветки/тега |

Архитектура — `linux/amd64`. На arm64-хосте (Ampere, Raspberry) образ собирается локально:
раскомментируйте `build:` у `yomibako` в `compose.yml` и выполните `docker compose build yomibako`
(сборка arm64 под QEMU на GitHub падает на `npm install` в образе — SIGILL, поэтому в релизе
её нет).

- **Пакет должен быть публичным**, иначе `docker compose up -d` на чужой машине получит
  `unauthorized`. Один раз после первого релиза: GitHub → Packages → `yomibako` → Package settings
  → Change visibility → Public. Иначе нужен `docker login ghcr.io` на целевой машине.
- **Пиннинг:** `YOMIBAKO_IMAGE=ghcr.io/mimic890/yomibako:0.1.0` в `.env` вместо `:latest`, если
  нужны воспроизводимые передеплои.
- **Сборка на месте** (без реестра или на arm64): раскомментировать `build:` у сервиса `yomibako` в
  `compose.yml` и выполнить `docker compose build yomibako` — дальше `up -d` возьмёт локальный
  образ.

**Версия miwayomi зафиксирована.** `MIWAYOMI_IMAGE` и `MIWAYOMI_BUILD_CONTEXT` по умолчанию
указывают на один и тот же релиз (`0.2.9` / тег `v0.2.9`, коммит `bf18765`), так что пересборка
не подтянет молча новый `main`. Если реестр не отдаёт образ, Compose собирает его из этого
upstream-тега (Gradle, несколько минут). Для продакшена соберите образ один раз, положите в свой
registry и работайте по immutable-тегу:

```bash
docker compose build miwayomi                                              # один раз
docker tag ghcr.io/miwayomi/miwayomi:0.2.9 ghcr.io/<you>/miwayomi:0.2.9-1
docker push ghcr.io/<you>/miwayomi:0.2.9-1
# в .env: MIWAYOMI_IMAGE=ghcr.io/<you>/miwayomi:0.2.9-1
docker compose up -d
```

При обновлении miwayomi меняйте оба значения вместе (образ и build context) и проверяйте, что
`/health` и `/api/v1/sources` отвечают как раньше: Yomibako завязан на этот REST API.

### Важные переменные

| Переменная | Зачем |
|---|---|
| `YOMIBAKO_TOKEN` | общий секрет с BookOrbit; пусто — сервис без авторизации |
| `DOCKER_GID` | GID владельца `/var/run/docker.sock`; без него `docker start miwayomi` упадёт с `permission denied` |
| `MIWAYOMI_URL` | адрес API miwayomi из контейнера (`http://miwayomi:4567`) |
| `YOMIBAKO_HOST`, `YOMIBAKO_PORT` | адрес прослушивания (в образе уже `0.0.0.0:7345`) |
| `YOMIBAKO_CONFIG` | путь к своему конфигу вместо `/etc/yomibako/config.json` |
| `YOMIBAKO_IMAGE` | образ Yomibako; по умолчанию `ghcr.io/mimic890/yomibako:latest` |
| `MIWAYOMI_IMAGE`, `MIWAYOMI_BUILD_CONTEXT` | образ miwayomi и upstream-тег для fallback-сборки (оба зафиксированы на `0.2.9`) |
| `FLARESOLVERR_URL` | внутри сервиса miwayomi; пусто — Cloudflare не решается |

Свой конфиг подключается монтированием; рядом можно положить `cookies.txt` для LNReader:

```yaml
    volumes:
      - ./config.json:/config/config.json:ro
      - ./cookies.txt:/config/cookies.txt:ro
    environment:
      YOMIBAKO_CONFIG: /config/config.json
```

### Проверка

```bash
curl -s http://127.0.0.1:7345/health          # токен не нужен
docker compose ps                             # yomibako healthy, miwayomi running|exited
docker compose logs -f yomibako
```

`/health` отдаёт `{"ok":true,"runtimes":{"mihon":{...,"running":true},"lnreader":{...}}}`.
`"running": false` — нормальное состояние простоя: следующий поиск поднимет контейнер.
У LNReader `"available": false` означает, что CLI не найден.

### Безопасность

- **Токен обязателен, если порт виден кому-то, кроме вас.** `YOMIBAKO_TOKEN` попадает в настройки
  каждого источника BookOrbit; без него любой, кто дотянется до порта, сможет искать и качать.
- **Порт по умолчанию только на loopback** (`127.0.0.1:7345`). Наружу — через reverse proxy с
  TLS или SSH-туннель; при публикации на `0.0.0.0` сначала включите токен.
- **Docker-сокет = root на хосте.** Yomibako запускает и останавливает контейнеры, поэтому монтирует
  `/var/run/docker.sock`. Не давайте этот сервис недоверенным пользователям и не публикуйте его без
  аутентификации.
- **`/health` открыт без токена** и показывает только флаги рантаймов; ничего не запускает.
- **Токен в URL скачивания** попадает в логи BookOrbit и reverse proxy. Если это неприемлемо,
  не задавайте токен и держите порт на loopback.
- **miwayomi на `127.0.0.1:4567`** — это WebUI с установкой сторонних расширений; наружу его
  выставлять не нужно.
- Не запускайте контейнеры от root и не расширяйте права: образ уже работает под UID 10001, а
  доступ к сокету даётся только через `group_add`.

## Проверка

```bash
(cd proxy-go && go vet ./... && go test ./... && go build -o yomibako .)
node proxy/selfcheck.mjs         # старая Node-версия: ZIP/CRC, имена и контракт плагинов
docker compose config --quiet    # синтаксис compose
docker build -t yomibako:local proxy-go   # сборка образа без compose
```

CI (`.github/workflows/ci.yml`) прогоняет `gofmt`, `go vet`, `go mod tidy -diff`, `go test`,
`go build`, `node proxy/selfcheck.mjs`, `docker compose config` и сборку образа.
Release (`.github/workflows/release.yml`) повторяет `go vet`/`go test` и публикует образ в GHCR.

## Ограничения и риски

- **BookOrbit Plugin API v1 больше не запрашивает комиксы**, только ebook/audiobook. Плагин
  Mihon отдаёт CBZ с `mediaKinds: ['ebook']` и `format: 'cbz'`; если BookOrbit отфильтрует
  формат — это первое место, которое надо смотреть.
- **Скачивается весь тайтл целиком.** У манги это сотни глав и гигабайты; `maxChapters`
  ограничивает, но полноценного «выбрать главы» в indexer-контракте нет. У LNReader то же самое:
  `download` тянет всю книгу, паузы и докачки между перезапусками нет.
- **Первый запрос после простоя дольше**: JVM miwayomi стартует десятки секунд. У BookOrbit
  есть свой дедлайн на запрос; при таймауте поиск надо повторить — контейнер к этому моменту уже
  поднят, и повторный запрос будет быстрым.
- **Страницы некоторых источников требуют заголовков/Cloudflare.** Картинки идут через
  `/api/v1/proxy` miwayomi, но если источник без FlareSolverr не отвечает — нужен FlareSolverr
  (поле `FLARESOLVERR_URL` в compose; sidecar в этот файл не входит, его надо добавить самому).
- **LNReader CLI — prerelease.** Флаги и JSON-вывод могут поменяться; ломается в одном месте —
  `proxy-go/lnreader.go` (и `proxy/runtimes/lnreader.mjs` у Node-версии). Версия в образе
  закреплена (`@lnreader/cli@0.1.1`), обновляйте её осознанно.
- **Прокси может запускать контейнеры и скачивать файлы**, поэтому по умолчанию слушает только
  localhost. Наружу — только с `token` и за обратным прокси.
- **Docker-сокет в контейнере** даёт Yomibako права уровня root на хосте: он может запускать и
  останавливать любые контейнеры, а не только `miwayomi`. Альтернатива — запускать Yomibako на
  хосте и не монтировать сокет.
- **Пакет GHCR по умолчанию приватный.** Пока видимость не переключена на Public (или на машине
  нет `docker login ghcr.io`), `docker compose up -d` не сможет забрать `yomibako`.
- **Образ miwayomi не гарантированно доступен в реестре.** Compose умеет собрать его из
  upstream-репозитория (`MIWAYOMI_BUILD_CONTEXT`, зафиксирован на `v0.2.9`), но это долгая
  Gradle-сборка; для продакшена лучше один раз собрать и хранить свой образ по immutable-тегу.
- **Yomibako завязан на REST API miwayomi.** Эндпойнты `/api/v1/sources`, `/manga/{id}/search`,
  `/chapters`, `/pages`, `/proxy` и форматы ответов могут измениться в новом релизе miwayomi
  (или не совпасть с Suwayomi — у него другой API). После обновления проверяйте `/health` и
  один тестовый поиск.
- **Состояние не переносится между перезапусками.** Задания на скачивание живут в памяти, файлы —
  во временном каталоге контейнера с TTL (`fileTtlMinutes`): после рестарта незавершённые загрузки
  теряются, а готовые надо запрашивать заново. Сами тайтлы BookOrbit забирает сразу.
- **Один пользователь, без лимитов.** Нет квот, рейт-лимитов и параллельных слотов на скачивание:
  несколько одновременных grab'ов делят `imageConcurrency`/`concurrency` и память контейнера
  (`mem_limit` 512m у Yomibako, 768m у miwayomi). При OOM-килле загрузка просто упадёт.
- **Диск.** CBZ/EPUB лежат в `/tmp` контейнера до истечения TTL; для больших тайтлов нужен запас
  места, иначе сборка упадёт на записи файла.
- **Ошибки источников приходят как есть**: текст исключения расширения/CLI уходит в BookOrbit
  строкой `{error}`. Для отладки — `docker compose logs -f yomibako`.
- Плагины и рантаймы запускают чужой код расширений. Ответственность за источники и
  использование контента — на владельце инстанса.
