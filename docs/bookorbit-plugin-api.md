# Проверка BookOrbit Plugin API для RanobeLib

Проверено по официальному репозиторию:

- `https://github.com/bookorbit/bookorbit`
- коммит `c77966cc529423e993fae8a298aa77048b43bba2` (2026-11-12)
- контракт: `packages/plugin-api/src/index.ts`, `PLUGIN_API_VERSION = 1`
- загрузчик: `server/src/modules/book-request/indexers/plugins/plugin-loader.service.ts`
- хост: `server/src/modules/book-request/indexers/plugins/plugin-host.ts`
- прямое скачивание: `server/src/modules/book-request/fulfillment/direct-download.service.ts`
- импорт EPUB: `server/src/modules/upload/upload.service.ts`,
  `server/src/modules/metadata/metadata-extraction.service.ts`,
  `server/src/modules/metadata/lib/opf-parser.ts`,
  `server/src/modules/metadata/lib/cover-epub.ts`

## Что плагин умеет

### HTTP

`PluginHost.fetch(url, init)` — единственный предусмотренный путь в сеть:

```ts
fetch(url: string, init?: PluginRequestInit): Promise<Response>;
```

`packages/plugin-api/src/index.ts:183`.

### Поиск

```ts
search(
  query: PluginReleaseQuery,
  config: PluginIndexerConfig,
  host: PluginHost,
  signal: AbortSignal,
): Promise<PluginReleaseCandidate[]>;
```

`packages/plugin-api/src/index.ts:247`.

`PluginReleaseCandidate` содержит `guid`, `title`, `bookTitle`, `downloadUrl`, `infoUrl`,
`sizeBytes`, `seeders`, `leechers`, `format`, `language`, `author`, `isbn`, `publishedAt` и
некоторые другие поля.

В нём **нет** `description` и `coverUrl` (`packages/plugin-api/src/index.ts:93-138`).
Описание, обложка и остальные поля карточки появляются в BookOrbit только после импорта
файла книги — из OPF EPUB.

### Передача файла

Есть два взаимоисключающих хука:

```ts
fetchTorrentFile?(...): Promise<Uint8Array>;
resolveFile?(...): Promise<PluginReleaseFile>;
```

`packages/plugin-api/src/index.ts:250-252`.

`PluginReleaseFile`:

```ts
interface PluginReleaseFile {
  url: string;
  fileName: string;
  sizeBytes: number | null;
  format: string;
}
```

`packages/plugin-api/src/index.ts:141-147`.

`fetchTorrentFile` не подходит для EPUB: его `Uint8Array` далее разбирается как `.torrent` и
передаётся torrent-клиенту. `resolveFile` не принимает байты — только URL.

### Почему нельзя отдать EPUB

BookOrbit скачивает URL из `resolveFile` самостоятельно:

- `ensureSafeUrl` разрешает только `http:` и `https:` —
  `server/src/common/utils/ssrf.utils.ts:5,35`;
- direct-download запрещает приватные и локальные адреса:
  `const ALLOW_PRIVATE = false;` — `direct-download.service.ts:99`;
- проверка применяется к URL при добавлении загрузки —
  `direct-download.service.ts:161`.

Следовательно, нельзя вернуть:

- `data:` URL с EPUB;
- `file:` URL;
- URL на `127.0.0.1`/localhost с локальным сервером;
- URL на контейнер в приватной сети.

Публичный HTTP-сервер, который отдаёт EPUB, решил бы проблему, но это отдельный сервис,
который в рамках задачи запрещён.

## Что BookOrbit делает с EPUB

BookOrbit поддерживает EPUB как формат импорта (`packages/types/src/upload.ts`) и умеет читать
из OPF:

- `title`, `subtitle`, `description`;
- `authors`;
- `language`, `publisher`, даты;
- `seriesName`, `seriesIndex`;
- `genres`, `tags`;
- ISBN;
- обложку.

См. `server/src/modules/metadata/lib/opf-parser.ts` и
`server/src/modules/metadata/lib/cover-epub.ts`.

Поэтому корректно собранный EPUB с OPF и обложкой дал бы в BookOrbit полноценную карточку и
обложку после импорта. Не хватает только способа передать этот EPUB плагину в BookOrbit.

## RanobeLib

Проверенные живые эндпоинты:

| Назначение | Запрос |
|---|---|
| Поиск | `GET https://api.cdnlibs.org/api/manga/?site_id[0]=3&q=...&page=N` |
| Карточка | `GET https://api.cdnlibs.org/api/manga/{slug}?fields[]=summary&fields[]=genres&fields[]=tags&fields[]=authors&fields[]=status_id&fields[]=artists` |
| Главы | `GET https://api.cdnlibs.org/api/manga/{slug}/chapters` |
| Текст главы | `GET https://api.cdnlibs.org/api/manga/{slug}/chapter?number=...&volume=...&branch_id=...` |

Обязательные заголовки, которые использует актуальный LNReader-плагин:

- `Accept: application/json`
- `Referer: https://ranobelib.me`
- `Origin: https://ranobelib.me/`
- `Site-Id: 3`

Ответ поиска содержит `links`, `meta.current_page`, `meta.has_next_page` — пагинация есть.
Ответ API ограничен `x-ratelimit-limit: 100`.

Проверенные предполагаемые эндпоинты экспорта вернули `404`:

- `/api/manga/{slug}/download`
- `/api/manga/{slug}/export`
- `/api/manga/{slug}/chapters/download`

SPA-маршруты `/ru/book/{slug}/download` отдают HTML приложения, а не файл книги.

Известные внешние загрузчики RanobeLib (например, `zeroma25/ranobelib-downloader`,
`SayatYuss/ranobe-downloader`) сами обходят главы через API и собирают EPUB локально.

## Условия разблокировки

1. В BookOrbit появляется хук передачи сгенерированных байтов:
   например, `fetchFile(): Promise<Uint8Array>` или `resolveFile()` с полем `bytes`.
2. Либо RanobeLib предоставляет публичный стабильный EPUB/FB2 URL.

До этого реализация нативного плагина с полным сценарием невозможна без отдельного
HTTP-сервиса.

## Что реализовано в этом репозитории

`plugins/ranobelib/src/index.mjs` — изолированный модуль, не плагин BookOrbit. У него нет
`apiVersion`, `search`/`resolveFile` на верхнем уровне и default-export с plugin-полями, поэтому
загрузчик BookOrbit не примет его за индексер.

Модуль реализует:

- `search(query, { page, limit })` — поиск и пагинация;
- `getTitle(slug)` — карточка и метаданные;
- `getChapters(slug)` — главы, ветки, объединение и сортировка;
- `getChapter(slug, chapter)` — текст главы и изображения;
- `getCover(titleOrSlug)` — обложка;
- `buildBook(titleOrSlug, { branch, maxChapters, includeImages })` — EPUB 3.

Сборка EPUB не использует внешних зависимостей и проверена `epubcheck 5.4.0`:
`0 fatal / 0 error / 0 warning` на обезличенных фикстурах и на книге, собранной по живым
данным RanobeLib.

Тесты: `npm test` (27 тестов; epubcheck-тест включается через `EPUBCHECK_JAR`),
синтаксис: `npm run check`, пример книги: `npm run sample:epub`.

## Точная недостающая возможность API

Чтобы превратить этот модуль в рабочий плагин, BookOrbit нужен один из двух контрактов:

1. **Передача сгенерированных байтов из плагина.** Например:

   ```ts
   fetchFile?(release, config, host): Promise<Uint8Array>;
   ```

   или расширение `PluginReleaseFile` полем `bytes`/`content`. Тогда `resolveFile()` сможет
   вернуть EPUB, который модуль уже собирает в памяти.

2. **Публичный EPUB/FB2 URL от RanobeLib.** Тогда существующий `resolveFile({ url, fileName,
   sizeBytes, format })` достаточен, а BookOrbit сам скачает файл и извлечёт из него OPF-
   метаданные и обложку.

Чего недостаточно:

- `fetchTorrentFile()` — BookOrbit ожидает `.torrent` и передаёт его torrent-клиенту;
- `downloadUrl`/`infoUrl` в `PluginReleaseCandidate` — это ссылки, а не байты;
- локальный HTTP-сервер — direct-download запрещает приватные адреса;
- `data:`/`file:` URL — `ensureSafeUrl` разрешает только `http:`/`https:`;
- внешний временный хостинг — это отдельный сервис и обход ограничения, а не нативный плагин.
