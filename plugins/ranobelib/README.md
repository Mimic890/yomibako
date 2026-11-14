# RanobeLib: изолированный модуль для BookOrbit

Здесь лежит библиотека, которая уже умеет всё, что нужно будущему плагину:

- поиск по названию с пагинацией;
- карточку тайтла: основное и альтернативные названия, описание, авторов, художников,
  статус, жанры, теги, рейтинг, дату, возрастное ограничение, обложку и ссылку на источник;
- список глав с ветками переводов, объединением дублей и сортировкой по `index`;
- текст главы с безопасным преобразованием документа RanobeLib в XHTML;
- загрузку обложки и изображений глав;
- сборку EPUB 3 с OPF-метаданными, обложкой, навигацией, NCX и встроенными изображениями.

Но это **не** плагин BookOrbit: в корне `plugins/ranobelib/` намеренно нет `index.mjs` с
`apiVersion`, `search`, `test` и `resolveFile`, чтобы загрузчик BookOrbit не принял модуль за
рабочий индексер.

## Структура

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
```

## Использование

```js
import { RanobeLib } from './plugins/ranobelib/src/index.mjs';

const ranobe = new RanobeLib();
const search = await ranobe.search('Моя библиотека высоких технологий');
const title = await ranobe.getTitle(search.items[0].slug);
const chapters = await ranobe.getChapters(title.slug);
const chapter = await ranobe.getChapter(title.slug, chapters[0]);
const cover = await ranobe.getCover(title);

const book = await ranobe.buildBook(title, { maxChapters: 1 });
// book.bytes — готовый EPUB, book.warnings — пропущенные главы и изображения
```

`buildBook` по умолчанию идёт последовательно, с интервалом 600 мс между запросами, и не
скачивает всю книгу при поиске или открытии карточки. Полная книга из 250 глав займёт время;
для проверок используйте `maxChapters`.

## Проверки

```bash
npm test
npm run check
```

Тесты работают на обезличенных фикстурах и не обращаются к сети. Проверяются:

- параметры запросов, заголовки и пагинация поиска;
- карточка, альтернативные названия, описание, авторы, жанры, теги, обложка;
- объединение веток, сортировка глав;
- экранирование текста, ссылки, списки, цитаты и изображения;
- пустые ответы, HTTP-ошибки, битый JSON и пустые главы;
- структура EPUB, `mimetype`, OPF, nav, NCX, обложка и встроенные изображения;
- целостность ZIP через `unzip -t`.

Сборка примера и проверка epubcheck:

```bash
npm run sample:epub
EPUBCHECK_JAR=/path/to/epubcheck.jar npm test
```

Результат: `epubcheck 5.4.0` — `0 fatal / 0 error / 0 warning` для EPUB из фикстур и для
EPUB, собранного по живым данным RanobeLib.

## Почему модуль пока не подключён

BookOrbit Plugin API v1:

- `resolveFile()` возвращает только `{ url, fileName, sizeBytes, format }` и не принимает байты;
- `fetchTorrentFile()` возвращает байты, но BookOrbit разбирает их как `.torrent`;
- direct-download скачивает URL сам и разрешает только публичные `http:`/`https:`, поэтому
  локальный сервер и `data:`/`file:` URL не подходят;
- RanobeLib не отдаёт готовый EPUB по публичному URL — его API отдаёт карточку, главы и текст.

Точные ссылки на исходный код BookOrbit и номера строк — в
[../../docs/bookorbit-plugin-api.md](../../docs/bookorbit-plugin-api.md).

Плагин можно будет добавить как тонкий адаптер над `RanobeLib` после появления в BookOrbit
хука для сгенерированных файлов, например `fetchFile(): Promise<Uint8Array>` или
`resolveFile()` с полем `bytes`.
