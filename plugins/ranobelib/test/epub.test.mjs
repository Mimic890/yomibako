import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { buildEpub, bookFileName } from '../src/epub.mjs';
import { fixtureBytes } from './helpers/fixtures.mjs';
import { readZip } from './helpers/zip.mjs';

async function sample() {
  return {
    metadata: {
      title: 'Тестовый тайтл & продолжение',
      authors: ['Тестовый автор'],
      description: 'Описание с <угловыми> скобками и & амперсандом.\n\nВторой абзац.',
      genres: ['Научная фантастика', 'Приключения'],
      tags: ['Тестовый тег'],
      releaseDate: '2024-05-01',
      sourceUrl: 'https://ranobelib.me/ru/book/101--test-novel',
      slug: '101--test-novel',
    },
    chapters: [
      { title: 'Том 1 Глава 1 Первая глава', html: '<p>Текст &amp; продолжение.</p>' },
      { title: 'Том 1 Глава 2 Вторая глава', html: '<p>Второй текст.</p><img src="../images/chapter-001.png" alt="Иллюстрация"/>' },
    ],
    cover: { bytes: await fixtureBytes('cover.png'), mediaType: 'image/png' },
    images: [{ bytes: await fixtureBytes('chapter-image.png'), mediaType: 'image/png', fileName: 'chapter-001.png' }],
    modifiedAt: new Date('2026-11-14T00:00:00Z'),
  };
}

async function writeTempEpub(bytes, name = 'book.epub') {
  const directory = await mkdtemp(join(tmpdir(), 'ranobelib-epub-'));
  const file = join(directory, name);
  await writeFile(file, bytes);
  return { directory, file };
}

test('EPUB has the required container, mimetype and package documents', async () => {
  const input = await sample();
  const archive = readZip(buildEpub(input));

  assert.equal(archive.entries[0].name, 'mimetype');
  assert.equal(archive.entries[0].method, 0);
  assert.equal(archive.text('mimetype'), 'application/epub+zip');
  assert.equal(archive.has('META-INF/container.xml'), true);
  assert.equal(archive.has('OEBPS/content.opf'), true);
  assert.equal(archive.has('OEBPS/nav.xhtml'), true);
  assert.equal(archive.has('OEBPS/toc.ncx'), true);
  assert.equal(archive.has('OEBPS/style.css'), true);
  assert.equal(archive.has('OEBPS/images/cover.png'), true);
  assert.equal(archive.has('OEBPS/images/chapter-001.png'), true);
  assert.equal(archive.has('OEBPS/text/chapter-001.xhtml'), true);
  assert.equal(archive.has('OEBPS/text/chapter-002.xhtml'), true);

  const container = archive.text('META-INF/container.xml');
  assert.match(container, /full-path="OEBPS\/content\.opf"/);
  assert.match(container, /media-type="application\/oebps-package\+xml"/);
});

test('package metadata carries title, authors, description, subjects and cover', async () => {
  const input = await sample();
  const archive = readZip(buildEpub(input));
  const opf = archive.text('OEBPS/content.opf');

  assert.match(opf, /<dc:title>Тестовый тайтл &amp; продолжение<\/dc:title>/);
  assert.match(opf, /<dc:creator>Тестовый автор<\/dc:creator>/);
  assert.match(opf, /<dc:description>Описание с &lt;угловыми&gt; скобками и &amp; амперсандом/);
  assert.match(opf, /<dc:subject>Научная фантастика<\/dc:subject>/);
  assert.match(opf, /<dc:subject>Тестовый тег<\/dc:subject>/);
  assert.match(opf, /<dc:date>2024-05-01<\/dc:date>/);
  assert.match(opf, /<dc:source>https:\/\/ranobelib\.me\/ru\/book\/101--test-novel<\/dc:source>/);
  assert.match(opf, /<meta property="dcterms:modified">2026-11-14T00:00:00Z<\/meta>/);
  assert.match(opf, /<item id="cover-image" href="images\/cover\.png" media-type="image\/png" properties="cover-image"\/>/);
  assert.match(opf, /<spine toc="ncx">/);
  assert.match(opf, /<itemref idref="chapter-1"\/>/);
  assert.match(opf, /<itemref idref="chapter-2"\/>/);
});

test('navigation and chapter documents point at the generated files', async () => {
  const input = await sample();
  const archive = readZip(buildEpub(input));
  const nav = archive.text('OEBPS/nav.xhtml');
  const chapter = archive.text('OEBPS/text/chapter-001.xhtml');

  assert.match(nav, /<nav epub:type="toc"/);
  assert.match(nav, /href="text\/chapter-001\.xhtml"/);
  assert.match(nav, /href="text\/chapter-002\.xhtml"/);
  assert.match(chapter, /<section epub:type="chapter" id="chapter-1">/);
  assert.match(chapter, /<h1>Том 1 Глава 1 Первая глава<\/h1>/);
  assert.match(chapter, /<p>Текст &amp; продолжение\.<\/p>/);
  assert.match(archive.text('OEBPS/text/chapter-002.xhtml'), /<img src="\.\.\/images\/chapter-001\.png" alt="Иллюстрация"\/>/);
});

test('the generated file is a valid ZIP readable by unzip', async (t) => {
  const input = await sample();
  const bytes = buildEpub(input);
  const { directory, file } = await writeTempEpub(bytes);
  try {
    const result = spawnSync('unzip', ['-t', file], { encoding: 'utf8' });
    if (result.error?.code === 'ENOENT') {
      t.skip('unzip is not installed');
      return;
    }
    assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
    assert.match(result.stdout, /No errors detected/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('epubcheck validates the generated file when a jar is provided', async (t) => {
  const jar = process.env.EPUBCHECK_JAR;
  if (!jar) {
    t.skip('set EPUBCHECK_JAR to run epubcheck');
    return;
  }
  const input = await sample();
  const { directory, file } = await writeTempEpub(buildEpub(input));
  try {
    const result = spawnSync('java', ['-jar', jar, file], { encoding: 'utf8' });
    assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
    assert.match(result.stdout, /No errors or warnings detected|0 errors/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('the builder rejects input that cannot produce a valid book', async () => {
  const input = await sample();
  assert.throws(() => buildEpub({ ...input, metadata: { ...input.metadata, title: '  ' } }), /title is required/);
  assert.throws(() => buildEpub({ ...input, chapters: [] }), /at least one chapter/);
  assert.throws(
    () => buildEpub({ ...input, images: [input.images[0], input.images[0]] }),
    /Duplicate ZIP entry|media type/,
  );
  assert.throws(
    () => buildEpub({ ...input, cover: { bytes: new Uint8Array([1, 2, 3]), mediaType: 'application/pdf' } }),
    /cover image type is not supported/,
  );
});

test('bookFileName strips path separators and keeps the epub extension', () => {
  assert.equal(bookFileName('Тест/Тайтл: 1', '101--test'), 'Тест-Тайтл- 1 (101--test).epub');
  assert.equal(bookFileName('   ', null), 'ranobelib-book.epub');
});
