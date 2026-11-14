# Yomibako → модуль RanobeLib для BookOrbit

Репозиторий очищен от прежней архитектуры runtime-прокси: удалены `proxy-go/`, Node-прокси,
`compose.yml`, Docker/CI и плагины Mihon/LNReader, которые ходили во внешний сервис.

Вместо них добавлен изолированный модуль RanobeLib: поиск, карточка тайтла, метаданные,
обложка, главы, текст глав и сборка EPUB. Модуль **не подключён** как плагин BookOrbit, потому
что действующий Plugin API v1 не позволяет `resolveFile()` вернуть сгенерированный EPUB.
Подробный разбор — в [docs/bookorbit-plugin-api.md](docs/bookorbit-plugin-api.md).

## Что было удалено

| Путь | Что это было |
|---|---|
| `proxy-go/` | Go-сервис Yomibako, управлял miwayomi и вызывал LNReader CLI |
| `proxy/` | прежняя Node-версия того же сервиса |
| `compose.yml` | стек `yomibako` + `miwayomi` |
| `.env.example` | переменные удалённого стека |
| `.github/workflows/ci.yml` | CI для Go-прокси, Compose и Docker-образа |
| `.github/workflows/release.yml` | публикация образа Yomibako в GHCR |
| `plugins/mihon/index.mjs` | плагин-обёртка над miwayomi |
| `plugins/lnreader/index.mjs` | плагин-обёртка над LNReader CLI |

Старые файлы остаются в истории git. Данные установок, Docker-тома, cookies и секреты вне
репозитория не затрагивались.

## Что осталось

```
plugins/ranobelib/
  src/
    index.mjs       # RanobeLib: search, getTitle, getChapters, getChapter, getCover, buildBook
    client.mjs      # HTTP-клиент api.cdnlibs.org с ограничением частоты
    normalize.mjs   # ответы API -> модели модуля
    document.mjs    # документы RanobeLib -> безопасный текст/XHTML
    epub.mjs        # EPUB 3 и ZIP без внешних зависимостей
    media.mjs       # определение типа изображения
    errors.mjs      # RanobeLibError с кодами
  test/
    fixtures/       # обезличенные ответы API и тестовые PNG
    helpers/        # fetch-заглушка, чтение ZIP, роутер фикстур
    *.test.mjs
  scripts/
    build-sample.mjs
docs/
  bookorbit-plugin-api.md
package.json
```

В корне `plugins/ranobelib/` намеренно нет `index.mjs` с `apiVersion`/`search`/`resolveFile`:
иначе загрузчик BookOrbit принял бы библиотеку за рабочий индексер.

## Возможности модуля

- поиск по названию с пагинацией по `api.cdnlibs.org`;
- карточка: основное и альтернативные названия, описание, авторы, художники, статус,
  жанры, теги, рейтинг, дата, возрастное ограничение, обложка, ссылка на источник;
- главы с ветками переводов, объединением дублей и сортировкой по `index`;
- текст главы: документ RanobeLib преобразуется в безопасный XHTML; поддерживаются абзацы,
  заголовки, списки, цитаты, ссылки, выделение и изображения;
- загрузка обложки и изображений глав;
- EPUB 3: `mimetype`, `container.xml`, OPF, nav, NCX, обложка, встроенные изображения;
- ошибки HTTP/сети/JSON, пустые ответы, пустые главы и пропуски оформлены как
  `RanobeLibError` или предупреждения.

Подробности и пример использования — в [plugins/ranobelib/README.md](plugins/ranobelib/README.md).

## Почему модуль не подключён как плагин

Официальный контракт BookOrbit (`@bookorbit/plugin-api`, `PLUGIN_API_VERSION = 1`) даёт плагину:

- `host.fetch(...)` — HTTP-запросы к RanobeLib;
- `search(...)` — поиск и ограниченные метаданные релиза;
- `resolveFile(...)` — **только URL** файла, имя, размер и формат;
- `fetchTorrentFile(...)` — байты, но исключительно `.torrent` для torrent-клиента.

Плагин не может вернуть байты EPUB. `resolveFile` не принимает содержимое файла, а BookOrbit
скачивает переданный URL сам, разрешая только публичные `http:`/`https:` адреса. Локальный
сервер рядом с BookOrbit тоже не подходит: direct-download явно запрещает приватные адреса.

При этом RanobeLib не отдаёт готовый EPUB/FB2 по публичному URL: его API отдаёт карточку,
главы и текст главы, а EPUB приходится собирать самостоятельно.

Итог: без изменения API BookOrbit или без отдельного сервиса, который вы запретили, нативный
плагин не может выполнить полный сценарий «поиск → карточка → главы → EPUB → импорт».
Модуль готов к подключению, как только появится хук для сгенерированных файлов.

## Проверки

```bash
npm test
npm run check
```

Тесты работают на обезличенных фикстурах, не обращаются к сети и покрывают поиск, карточку,
главы, текст, изображения, ошибки, пустые данные и структуру EPUB. Дополнительно проверяется
целостность ZIP через `unzip -t` и, если задан `EPUBCHECK_JAR`, полная валидация epubcheck:

```bash
npm run sample:epub
EPUBCHECK_JAR=/path/to/epubcheck.jar npm test
```

Проверено:

- `npm test` — 27 тестов; 26 проходят без внешних инструментов, 27-й (epubcheck)
  включается переменной `EPUBCHECK_JAR`;
- `npm run check` — синтаксическая проверка всех модулей;
- `epubcheck 5.4.0` — `0 fatal / 0 error / 0 warning` для EPUB из фикстур;
- `epubcheck 5.4.0` — `0 fatal / 0 error / 0 warning` для EPUB, собранного по живым данным
  RanobeLib (поиск, карточка, главы, текст главы, обложка; одна глава, без полной книги).

## Атрибуция

Актуальный LNReader-плагин RanobeLib (`RLIB`, MIT License) изучен как источник сведений об
эндпоинтах, но его код в этот репозиторий не переносился. При будущем заимствовании нужно
сохранить уведомление MIT и указать авторов `lnreader-plugins`.
