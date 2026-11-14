import assert from 'node:assert/strict';
import test from 'node:test';

import { RanobeLibClient } from '../src/client.mjs';
import { RanobeLibError } from '../src/errors.mjs';
import { fixtureBytes, fixtureJson } from './helpers/fixtures.mjs';
import { createFetchStub, jsonResponse, binaryResponse } from './helpers/http.mjs';
import { createRanobeLibFixtureFetch } from './helpers/ranobelib-stub.mjs';

async function fixtures() {
  return {
    search: await fixtureJson('search.json'),
    title: await fixtureJson('title.json'),
    chapters: await fixtureJson('chapters.json'),
    chapter: await fixtureJson('chapter.json'),
    cover: await fixtureBytes('cover.png'),
    image: await fixtureBytes('chapter-image.png'),
  };
}

function clientFor(fetch, options = {}) {
  return new RanobeLibClient({ fetchImpl: fetch, minRequestIntervalMs: 0, ...options });
}

test('search sends the API query and maps pagination and covers', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const client = clientFor(stub.fetch);

  const result = await client.search({ query: '  Тестовый тайтл  ' });

  assert.equal(stub.calls.length, 1);
  const call = stub.calls[0];
  assert.equal(call.url.origin, 'https://api.cdnlibs.org');
  assert.equal(call.url.pathname, '/api/manga/');
  assert.equal(call.url.searchParams.get('site_id[0]'), '3');
  assert.equal(call.url.searchParams.get('q'), 'Тестовый тайтл');
  assert.equal(call.url.searchParams.get('page'), '1');
  assert.equal(call.init.headers.Accept, 'application/json');
  assert.equal(call.init.headers.Referer, 'https://ranobelib.me/');
  assert.equal(call.init.headers['Site-Id'], '3');

  assert.equal(result.items.length, 2);
  const [first, second] = result.items;
  assert.equal(first.title, 'Тестовый тайтл');
  assert.deepEqual(first.alternativeTitles, ['Test Novel']);
  assert.equal(first.cover.url, 'https://cdn.example.invalid/covers/101.png');
  assert.equal(first.cover.thumbnailUrl, 'https://cdn.example.invalid/covers/101-thumb.png');
  assert.equal(first.status, 'Завершён');
  assert.equal(first.type, 'Япония');
  assert.equal(first.rating, 8.5);
  assert.equal(first.url, 'https://ranobelib.me/ru/book/101--test-novel');
  assert.equal(second.isLicensed, true);
  assert.equal(result.pagination.page, 1);
  assert.equal(result.pagination.hasNextPage, true);
  assert.equal(result.pagination.nextPage, 2);
});

test('search can limit one page without changing the API page', async () => {
  const data = await fixtures();
  const pageTwo = structuredClone(data.search);
  pageTwo.meta.current_page = 2;
  pageTwo.meta.has_next_page = false;
  pageTwo.links.next = null;
  const stub = createRanobeLibFixtureFetch({ ...data, search: pageTwo });
  const client = clientFor(stub.fetch);

  const result = await client.search({ query: 'тайтл', page: 2, limit: 1 });

  assert.equal(stub.calls[0].url.searchParams.get('page'), '2');
  assert.equal(result.items.length, 1);
  assert.equal(result.pagination.page, 2);
  assert.equal(result.pagination.hasNextPage, false);
  assert.equal(result.pagination.nextPage, null);
});

test('title card maps description, authors, genres and the source URL', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const client = clientFor(stub.fetch);

  const title = await client.getTitle('101--test-novel');

  const request = stub.calls[0].url;
  assert.equal(request.pathname, '/api/manga/101--test-novel');
  assert.deepEqual(request.searchParams.getAll('fields[]'), ['summary', 'genres', 'tags', 'teams', 'authors', 'status_id', 'artists']);

  assert.equal(title.title, 'Тестовый тайтл');
  assert.deepEqual(title.alternativeTitles, ['Test Novel']);
  assert.match(title.description, /Первое описание тестового тайтла/);
  assert.match(title.description, /<угловыми>/);
  assert.deepEqual(title.authors, ['Тестовый автор']);
  assert.deepEqual(title.artists, ['Тестовый художник']);
  assert.deepEqual(title.genres, ['Научная фантастика', 'Приключения']);
  assert.deepEqual(title.tags, ['ГГ мужчина', 'Машинный перевод']);
  assert.deepEqual(title.teams, ['Основная команда', 'Альтернативная команда']);
  assert.equal(title.status, 'Завершён');
  assert.equal(title.scanlateStatus, 'Заброшен');
  assert.equal(title.type, 'Япония');
  assert.equal(title.rating, 8.5);
  assert.equal(title.releaseDate, '2024-05-01');
  assert.equal(title.ageRestriction, '16+');
  assert.equal(title.isLicensed, false);
  assert.equal(title.cover.url, 'https://cdn.example.invalid/covers/101.png');
  assert.equal(title.sourceUrl, 'https://ranobelib.me/ru/book/101--test-novel');
});

test('chapter list flattens and merges branches and sorts by the source index', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const client = clientFor(stub.fetch);

  const chapters = await client.getChapters('101--test-novel');

  assert.equal(stub.calls[0].url.pathname, '/api/manga/101--test-novel/chapters');
  assert.deepEqual(
    chapters.map((chapter) => chapter.number),
    ['1', '1.5', '2'],
  );
  assert.equal(chapters[0].displayTitle, 'Том 1 Глава 1 Первая глава');
  assert.equal(chapters[0].branches.length, 3);
  assert.deepEqual(
    chapters[0].branches.map((branch) => branch.teamName),
    ['Основная команда', 'Альтернативная команда', 'Третья команда'],
  );
  assert.equal(chapters[1].displayTitle, 'Том 1 Глава 1.5 Полторы главы');
  assert.equal(chapters[2].index, 2);
});

test('chapter text is escaped, formatted and keeps image attachments', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const client = clientFor(stub.fetch);

  const chapter = await client.getChapter('101--test-novel', { volume: '1', number: '1', branchId: 7, title: 'Первая глава' });

  const request = stub.calls[0].url;
  assert.equal(request.pathname, '/api/manga/101--test-novel/chapter');
  assert.equal(request.searchParams.get('volume'), '1');
  assert.equal(request.searchParams.get('number'), '1');
  assert.equal(request.searchParams.get('branch_id'), '7');

  assert.match(chapter.text, /Текст с <опасными> скобками и & амперсандом/);
  assert.match(chapter.text, /Вторая строка/);
  assert.match(chapter.html, /<h2>Заголовок главы<\/h2>/);
  assert.match(chapter.html, /&lt;опасными&gt;/);
  assert.match(chapter.html, /&amp; амперсандом/);
  assert.match(chapter.html, /<strong>жирный<\/strong>/);
  assert.match(chapter.html, /<em>курсив<\/em>/);
  assert.match(chapter.html, /<blockquote>/);
  assert.match(chapter.html, /<ul>/);
  assert.match(chapter.html, /<img src="https:\/\/cdn\.example\.invalid\/chapters\/img-1\.png" alt="Иллюстрация"\/>/);
  assert.equal(chapter.images.length, 1);
  assert.equal(chapter.images[0].url, 'https://cdn.example.invalid/chapters/img-1.png');
  assert.equal(chapter.images[0].alt, 'Иллюстрация');
});

test('HTTP and parse failures become typed errors', async () => {
  const missing = createFetchStub(() => undefined);
  await assert.rejects(clientFor(missing.fetch).getTitle('999--missing'), (error) => {
    assert.ok(error instanceof RanobeLibError);
    assert.equal(error.code, 'notFound');
    assert.equal(error.status, 404);
    return true;
  });

  const invalidJson = createFetchStub(() => new Response('not json', { status: 200, headers: { 'content-type': 'application/json' } }));
  await assert.rejects(clientFor(invalidJson.fetch).search({ query: 'test' }), (error) => {
    assert.equal(error.code, 'parse');
    return true;
  });

  const invalidShape = createFetchStub(() => jsonResponse({ data: { nope: true } }));
  await assert.rejects(clientFor(invalidShape.fetch).search({ query: 'test' }), (error) => {
    assert.equal(error.code, 'parse');
    return true;
  });

  const noCalls = createFetchStub(() => jsonResponse({ data: [] }));
  await assert.rejects(clientFor(noCalls.fetch).search({ query: '   ' }), (error) => {
    assert.equal(error.code, 'invalid');
    return true;
  });
  assert.equal(noCalls.calls.length, 0);
});

test('requests are spaced by the configured interval', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  let now = 0;
  const waits = [];
  const client = new RanobeLibClient({
    fetchImpl: stub.fetch,
    minRequestIntervalMs: 100,
    now: () => now,
    sleepImpl: async (ms) => {
      waits.push(ms);
      now += ms;
    },
  });

  await client.search({ query: 'one' });
  await client.search({ query: 'two' });

  assert.deepEqual(waits, [100]);
});

test('image fetching detects PNG without a useful content type and rejects HTML', async () => {
  const data = await fixtures();
  const png = createFetchStub(() => binaryResponse(data.cover, { mediaType: 'application/octet-stream' }));
  const image = await clientFor(png.fetch).fetchImage('https://cdn.example.invalid/covers/101.png');
  assert.equal(image.mediaType, 'image/png');
  assert.equal(image.bytes.length, data.cover.length);

  const html = createFetchStub(() => new Response('<html>not an image</html>', { status: 200, headers: { 'content-type': 'text/html' } }));
  await assert.rejects(clientFor(html.fetch).fetchImage('https://cdn.example.invalid/cover'), (error) => {
    assert.equal(error.code, 'parse');
    return true;
  });
});

test('empty search and chapter responses are valid empty results', async () => {
  const data = await fixtures();
  const empty = {
    ...data,
    search: { data: [], links: {}, meta: { current_page: 1, per_page: 60, has_next_page: false } },
    chapters: { data: [] },
  };
  const stub = createRanobeLibFixtureFetch(empty);
  const client = clientFor(stub.fetch);

  const search = await client.search({ query: 'ничего' });
  assert.deepEqual(search.items, []);
  assert.equal(search.pagination.hasNextPage, false);
  assert.equal(search.pagination.nextPage, null);

  const chapters = await client.getChapters('101--test-novel');
  assert.deepEqual(chapters, []);
});
