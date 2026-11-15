# Yomibako

Загрузка тайтлов с книжных сайтов в EPUB. Сейчас поддерживается RanobeLib, дальше добавляются
другие источники.

Сервис собирает **том целиком** в один EPUB: обложка тома, метаданные, оглавление, иллюстрации
глав. Тома не перезаписываются - новый том это новый файл, поэтому прогресс чтения и метаданные
в библиотеке не ломаются.

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

`check` сравнивает список томов на сайте с тем, что записано в базе, и печатает новые. Ничего
не скачивает - сборка остаётся ручной.

Если задан `book_dock` в конфиге или флаг `--dock`, тома кладутся прямо в папку BookOrbit:
файл `<Название> - Том 01.epub`, дальше он сам читает метаданные и переносит книгу в библиотеку.
`--wait` ждёт, пока BookOrbit заберёт файл.

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

## Дальше

- запись готовых томов в папку `book-dock` BookOrbit;
- состояние в sqlite: что уже скачано, подписки, время последней проверки;
- бот: добавление тайтлов с телефона, прогресс загрузки, оповещения о новых томах.

## Старое

`plugins/ranobelib/` - прежний JS-модуль, оставлен как справка по API и как эталон разбора
документов. В работе не используется. Разбор ограничений Plugin API v1 -
в [docs/bookorbit-plugin-api.md](docs/bookorbit-plugin-api.md).
