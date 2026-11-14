import assert from 'node:assert/strict';
import test from 'node:test';

import { RanobeLib } from '../src/index.mjs';
import { fixtureBytes, fixtureJson } from './helpers/fixtures.mjs';
import { createRanobeLibFixtureFetch } from './helpers/ranobelib-stub.mjs';
import { readZip } from './helpers/zip.mjs';

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

async function moduleFor(fetch, options = {}) {
  return new RanobeLib({ fetchImpl: fetch, minRequestIntervalMs: 0, ...options });
}

test('buildBook produces an EPUB with cover, metadata and embedded chapter images', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const ranobe = await moduleFor(stub.fetch);

  const progress = [];
  const book = await ranobe.buildBook('101--test-novel', {
    modifiedAt: new Date('2026-11-14T00:00:00Z'),
    onProgress: (event) => progress.push(event.stage),
  });

  assert.equal(book.fileName, 'Тестовый тайтл (101--test-novel).epub');
  assert.equal(book.warnings.length, 0);
  assert.deepEqual(book.chapters, ['Том 1 Глава 1 Первая глава', 'Том 1 Глава 1.5 Полторы главы', 'Том 1 Глава 2 Вторая глава']);
  assert.deepEqual(progress, ['title', 'chapters', 'chapter', 'chapter', 'chapter', 'cover', 'epub']);

  const archive = readZip(book.bytes);
  assert.equal(archive.has('OEBPS/images/cover.png'), true);
  assert.equal(archive.has('OEBPS/images/chapter-001-01.png'), true);
  assert.equal(archive.has('OEBPS/images/chapter-002-01.png'), true);
  assert.equal(archive.has('OEBPS/images/chapter-003-01.png'), true);
  assert.equal(archive.has('OEBPS/text/title.xhtml'), true);

  const opf = archive.text('OEBPS/content.opf');
  assert.match(opf, /<dc:title>Тестовый тайтл<\/dc:title>/);
  assert.match(opf, /<dc:creator>Тестовый автор<\/dc:creator>/);
  assert.match(opf, /<dc:description>Первое описание тестового тайтла/);
  assert.match(opf, /<dc:subject>Научная фантастика<\/dc:subject>/);
  assert.match(opf, /properties="cover-image"/);

  const firstChapter = archive.text('OEBPS/text/chapter-001.xhtml');
  assert.match(firstChapter, /src="\.\.\/images\/chapter-001-01\.png"/);
  assert.doesNotMatch(firstChapter, /https:\/\/cdn\.example\.invalid\/chapters/);
  assert.match(firstChapter, /&lt;опасными&gt;/);

  const requestUrls = stub.calls.map((call) => call.url.href);
  assert.equal(requestUrls.filter((url) => url.includes('/chapter?')).length, 3);
  assert.equal(requestUrls.filter((url) => url.includes('/covers/101.png')).length, 1);
  assert.equal(requestUrls.filter((url) => url.includes('/chapters/img-1.png')).length, 3);
  for (const call of stub.calls.filter((item) => item.url.pathname.endsWith('/chapter'))) {
    const branch = call.url.searchParams.get('branch_id');
    if (call.url.searchParams.get('number') === '1.5') assert.equal(branch, '7');
    else assert.equal(branch, null);
  }
});

test('buildBook can limit chapters and pick a translation branch', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const ranobe = await moduleFor(stub.fetch);

  const oneChapter = await ranobe.buildBook('101--test-novel', { maxChapters: 1 });
  assert.equal(oneChapter.chapters.length, 1);
  assert.equal(readZip(oneChapter.bytes).has('OEBPS/text/chapter-002.xhtml'), false);

  const branchSeven = await ranobe.buildBook('101--test-novel', { branch: 7 });
  assert.deepEqual(branchSeven.chapters, ['Том 1 Глава 1 Первая глава', 'Том 1 Глава 1.5 Полторы главы']);
  assert.ok(branchSeven.warnings.some((warning) => warning.includes('Вторая глава')));

  const chapterCalls = stub.calls.filter((call) => call.url.pathname.endsWith('/chapter'));
  assert.ok(chapterCalls.some((call) => call.url.searchParams.get('branch_id') === '7'));
  assert.ok(!chapterCalls.some((call) => call.url.searchParams.get('number') === '2' && call.url.searchParams.get('branch_id') === '7'));
});

test('an empty chapter is skipped with a warning', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch({
    ...data,
    chapterByKey: (key) => (key === '1:2:null' ? { data: { id: 202, content: null, attachments: [] } } : data.chapter),
  });
  const ranobe = await moduleFor(stub.fetch);

  const book = await ranobe.buildBook('101--test-novel');

  assert.equal(book.chapters.length, 2);
  assert.ok(book.warnings.some((warning) => warning.includes('Вторая глава') && warning.includes('пустая')));
  assert.equal(readZip(book.bytes).has('OEBPS/text/chapter-003.xhtml'), false);
});

test('a chapter network failure aborts by default and can be collected explicitly', async () => {
  const data = await fixtures();
  const failing = createRanobeLibFixtureFetch({
    ...data,
    chapterByKey: (key) => (key === '1:2:null' ? { status: 503, json: { error: 'temporary' } } : data.chapter),
  });
  const ranobe = await moduleFor(failing.fetch);

  await assert.rejects(ranobe.buildBook('101--test-novel'), (error) => {
    assert.equal(error.status, 503);
    return true;
  });

  const continued = await ranobe.buildBook('101--test-novel', { continueOnError: true });
  assert.equal(continued.chapters.length, 2);
  assert.ok(continued.warnings.some((warning) => warning.includes('Вторая глава') && warning.includes('HTTP 503')));
});

test('getCover returns the detected image bytes', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch(data);
  const ranobe = await moduleFor(stub.fetch);

  const cover = await ranobe.getCover('101--test-novel');
  assert.equal(cover.mediaType, 'image/png');
  assert.equal(cover.bytes.length, data.cover.length);
});

test('the module does not expose a BookOrbit plugin entrypoint', async () => {
  const module = await import('../src/index.mjs');
  assert.equal(module.default, RanobeLib);
  assert.equal(module.default.apiVersion, undefined);
  assert.equal(typeof module.default.prototype.resolveFile, 'undefined');
  assert.equal(typeof module.default.prototype.search, 'function');
});

test('buildBook rejects a title with no chapters', async () => {
  const data = await fixtures();
  const stub = createRanobeLibFixtureFetch({ ...data, chapters: { data: [] } });
  const ranobe = await moduleFor(stub.fetch);

  await assert.rejects(ranobe.buildBook('101--test-novel'), (error) => {
    assert.equal(error.code, 'empty');
    return true;
  });
});
