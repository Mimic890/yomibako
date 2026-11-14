/**
 * Build a sample EPUB from the anonymised fixtures, without network access.
 *
 * Usage:
 *   node plugins/ranobelib/scripts/build-sample.mjs [output.epub]
 *
 * The output is for manual checks (for example with epubcheck) and is ignored by git.
 */

import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import { RanobeLib } from '../src/index.mjs';
import { fixtureBytes, fixtureJson } from '../test/helpers/fixtures.mjs';
import { createRanobeLibFixtureFetch } from '../test/helpers/ranobelib-stub.mjs';

const output = resolve(process.argv[2] ?? 'tmp/ranobelib-sample.epub');

const data = {
  search: await fixtureJson('search.json'),
  title: await fixtureJson('title.json'),
  chapters: await fixtureJson('chapters.json'),
  chapter: await fixtureJson('chapter.json'),
  cover: await fixtureBytes('cover.png'),
  image: await fixtureBytes('chapter-image.png'),
};

const stub = createRanobeLibFixtureFetch(data);
const ranobe = new RanobeLib({ fetchImpl: stub.fetch, minRequestIntervalMs: 0 });
const book = await ranobe.buildBook('101--test-novel', { modifiedAt: new Date('2026-11-14T00:00:00Z') });

await mkdir(dirname(output), { recursive: true });
await writeFile(output, book.bytes);

console.log(`Wrote ${output}`);
console.log(`Chapters: ${book.chapters.length}`);
if (book.warnings.length) console.log(`Warnings:\n- ${book.warnings.join('\n- ')}`);
