# Yomibako

Загрузка тайтлов с книжных сайтов в EPUB. Сейчас поддерживается RanobeLib, дальше добавляются
другие источники.

Сервис собирает **том целиком** в один EPUB: обложка тома, метаданные, оглавление, иллюстрации
глав. Тома не перезаписываются - новый том это новый файл, поэтому прогресс чтения и метаданные
в библиотеке не ломаются.

## Docker Compose

```yaml
services:
  yomibako:
    build:
      context: /srv/dev/yomibako
    image: yomibako:local
    container_name: yomibako
    restart: unless-stopped
    command: ["bot"]
    working_dir: /config
    volumes:
      - ./config:/config
      - ./data:/data
      - /srv/docker/bookorbit/data/state/book-dock:/book-dock
      - /srv/media/books:/library:ro
    environment:
      TZ: Europe/Moscow
```

Конфиг лежит в `./config/yomibako.yml`, база в `./data`, готовые тома уходят прямо в
`book-dock` BookOrbit. Пути внутри контейнера:

```yaml
database: /data/yomibako.db
book_dock: /book-dock
library_dir: /library
output_dir: /data/out
```

Запуск и обновление:

```bash
docker compose up -d --build
docker compose logs -f
```

## Установка

```bash
go build -o yomibako ./cmd/yomibako
```

Нужен только Go, внешних зависимостей нет.

## Использование

```bash
# поиск
yomibako search "повелитель тайн" -n 5

# карточка тайтла
yomibako info lord-of-the-mysteries

# какие есть тома
yomibako volumes lord-of-the-mysteries

# собрать том 1 в ./out/<Название>/Том 01/book.epub
yomibako add lord-of-the-mysteries --volume 1

# все тома
yomibako add lord-of-the-mysteries --all

# несколько томов, без иллюстраций, в свою папку
yomibako add lord-of-the-mysteries --volume 1,2 --no-images --out /tmp/books

# сразу в book-dock BookOrbit и дождаться импорта
yomibako add lord-of-the-mysteries --volume 1 --dock /srv/docker/bookorbit/data/state/book-dock --wait
```

## База и подписки

Сервис ведёт базу в SQLite: какие тайтлы добавлены, какие тома собраны и импортированы, на что
оформлена подписка.

```bash
yomibako list                          # что уже в базе
yomibako subscribe no-game-no-life-novel --interval 24
yomibako check                         # есть ли новые тома
yomibako unsubscribe no-game-no-life-novel
```

`check` сравнивает сайт с базой: какие тома новые и в каких уже собранных томах прибавилось
глав. Ничего не скачивает - сборка остаётся ручной.

`sync` сверяет базу с библиотекой на диске: книги, которые там лежат, помечаются
импортированными, а удалённые - «нет в библиотеке». Книги узнаются по идентификатору внутри
EPUB (`urn:ranobelib:<slug>:<том>`), а не по именам папок.

Если задан `book_dock` в конфиге или флаг `--dock`, тома кладутся прямо в папку BookOrbit:
файл `<Название> - Том 01.epub`, дальше он сам читает метаданные и переносит книгу в библиотеку.
По умолчанию файл остаётся в доке и ждёт подтверждения в интерфейсе BookOrbit: автофинализация
там выключена, библиотека меняется только вручную. `--wait` имеет смысл только если
автофинализацию включить - тогда команда дождётся, пока BookOrbit заберёт файл.

Ссылка тоже подходит: `yomibako add https://ranobelib.me/ru/book/20818--lord-of-the-mysteries`.

Флаг `--limit N` собирает только первые N глав тома - для проверки, что всё работает, не дожидаясь
полной загрузки.

## Конфигурация

`yomibako.yml` в текущей папке, `~/.config/yomibako/config.yml` или путь из `--config`:

```yaml
api_base: https://api.cdnlibs.org/api
site_base: https://ranobelib.me
site_id: 3
request_interval: 600ms
output_dir: out
include_images: true
```

`log_level` - `debug`, `info`, `warn` или `error`. На `info` видно каждую команду бота, начало и
конец сборки тома; на `debug` добавляются все HTTP-запросы к источнику.

Все поля необязательны. `request_interval` - пауза между запросами к сайту; уменьшать не стоит,
API отвечает 429.

Лицензированные и часть 18+ тайтлов API отдаёт только вошедшим. Нужен один из двух вариантов:

```yaml
auth:
  token: "<access_token из localStorage сайта>"
  # или
  cookies_file: /etc/yomibako/cookies.txt
```

Токен живёт около недели, потом его надо обновить. Куки из браузера живут дольше.

## Как это устроено

```
cmd/yomibako/                CLI
internal/source/             интерфейс источника: поиск, карточка, главы, текст главы, обложки
internal/sources/ranobelib/  реализация RanobeLib
internal/build/              группировка по томам и сборка
internal/epub/               запись EPUB 3
internal/config/             конфигурация
```

Источник - это интерфейс `source.Source`. Чтобы добавить сайт, нужен новый пакет в
`internal/sources` и регистрация в CLI; сборщик EPUB и группировка по томам общие.

## Проверки

```bash
go test ./...
go vet ./...
```

Тесты не ходят в сеть: ответы API подставляются фикстурами, HTTP-клиент проверяется на
`httptest`-сервере. Отдельно проверяется структура EPUB: `mimetype` первым и без сжатия,
корректный XML во всех файлах, метаданные серии, обложка и иллюстрации.

## Бот

```bash
yomibako bot
```

Отвечает только чату из `telegram.chat_id`. Команды: `/list`, `/add <название|ссылка>`,
`/search <запрос>`, `/check`, `/subscribe <ссылка> [часы]`, `/unsubscribe`, `/status`, `/help`.

`/add` сначала показывает список томов кнопками (уже собранные помечены галочкой), том
выбирается нажатием. Если по запросу несколько тайтлов, сначала предлагается выбрать тайтл.
Загрузки идут по одной в очереди, прогресс показывается правкой одного сообщения.

```yaml
telegram:
  token: "***"
  chat_id: 123456789
```

## Дальше

- запись готовых томов в папку `book-dock` BookOrbit;
- состояние в sqlite: что уже скачано, подписки, время последней проверки;
- бот: добавление тайтлов с телефона, прогресс загрузки, оповещения о новых томах.

## Старое

`plugins/ranobelib/` - прежний JS-модуль, оставлен как справка по API и как эталон разбора
документов. В работе не используется. Разбор ограничений Plugin API v1 -
в [docs/bookorbit-plugin-api.md](docs/bookorbit-plugin-api.md).
