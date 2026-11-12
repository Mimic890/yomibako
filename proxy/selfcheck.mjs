/**
 * Self-check for the parts that can be tested without Docker, BookOrbit or a live source.
 * Run: node proxy/selfcheck.mjs
 */
import assert from 'node:assert/strict';
import { readFile, rm } from 'node:fs/promises';
import http from 'node:http';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { ZipWriter } from './zip.mjs';
import { sanitizeName } from './util.mjs';
import mihonPlugin from '../plugins/mihon/index.mjs';
import lnreaderPlugin from '../plugins/lnreader/index.mjs';

const path = join(tmpdir(), `yomibako-selfcheck-${process.pid}.zip`);
const entries = [
  ['ch-0001/0001.jpg', Buffer.from([0xff, 0xd8, 0xff, 0x00, 0x11, 0x22])],
  ['ch-0001/0002.png', Buffer.from('not actually a png')],
];

const server = http.createServer(async (request, response) => {
  const send = (status, value) => {
    response.writeHead(status, { 'content-type': 'application/json' });
    response.end(JSON.stringify(value));
  };

  if (request.method === 'GET' && request.url === '/health') {
    return send(200, { ok: true, runtimes: { mihon: { enabled: true }, lnreader: { enabled: true, available: true } } });
  }

  const body = await new Promise((resolve) => {
    const chunks = [];
    request.on('data', (chunk) => chunks.push(chunk));
    request.on('end', () => resolve(JSON.parse(Buffer.concat(chunks).toString() || '{}')));
  });

  if (request.method === 'POST' && request.url === '/v1/search') {
    return send(200, { releases: [{ guid: 'work-1', title: 'Dune', author: 'Frank Herbert' }] });
  }
  if (request.method === 'POST' && request.url === '/v1/grab') {
    const isNovel = body.runtime === 'lnreader';
    return send(202, {
      id: 'job-1',
      fileName: isNovel ? 'Dune.epub' : 'Dune.cbz',
      format: isNovel ? 'epub' : 'cbz',
      url: '/v1/files/job-1',
    });
  }
  return send(404, { error: 'not found' });
});

await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const baseUrl = `http://127.0.0.1:${server.address().port}`;

const host = {
  fetch: (url, init) => fetch(url, init),
  fail: (failure, message) => Object.assign(new Error(message), { failure }),
  logger: { log() {}, warn() {} },
  buildSearchText: (query) => query.title,
  async saveCredential() {},
};

function config(source) {
  return { baseUrl, settings: { source, token: '' } };
}

try {
  const writer = await new ZipWriter(path).open();
  for (const [name, data] of entries) await writer.add(name, data);
  await writer.close();

  const zip = await readFile(path);
  const eocd = zip.length - 22;
  assert.equal(zip.readUInt32LE(eocd), 0x06054b50, 'EOCD signature');
  assert.equal(zip.readUInt16LE(eocd + 10), entries.length, 'entry count');

  const centralOffset = zip.readUInt32LE(eocd + 16);
  let offset = centralOffset;
  for (const [name, data] of entries) {
    assert.equal(zip.readUInt32LE(offset), 0x02014b50, 'central header signature');
    const nameLength = zip.readUInt16LE(offset + 28);
    assert.equal(zip.subarray(offset + 46, offset + 46 + nameLength).toString('utf8'), name, 'entry name');
    assert.equal(zip.readUInt32LE(offset + 20), data.length, 'entry size');

    const localOffset = zip.readUInt32LE(offset + 42);
    assert.equal(zip.readUInt32LE(localOffset), 0x04034b50, 'local header signature');
    const localNameLength = zip.readUInt16LE(localOffset + 26);
    const dataStart = localOffset + 30 + localNameLength;
    assert.deepEqual(zip.subarray(dataStart, dataStart + data.length), data, 'entry data');
    offset += 46 + nameLength;
  }

  assert.equal(sanitizeName('  Dune: Part / Two?  '), 'Dune Part Two');
  assert.equal(sanitizeName(''), 'book');

  for (const [plugin, source, format] of [[mihonPlugin, 'MangaLib', 'cbz'], [lnreaderPlugin, 'RLIB', 'epub']]) {
    assert.equal(plugin.apiVersion, 1);
    const releases = await plugin.search(
      { title: 'Dune', author: 'Frank Herbert', limit: 5 },
      config(source),
      host,
      new AbortController().signal,
    );
    assert.equal(releases.length, 1);
    assert.equal(releases[0].guid, 'work-1');
    assert.equal(releases[0].format, format);
    assert.equal(releases[0].seeders, null);

    const file = await plugin.resolveFile(releases[0], config(source), host);
    assert.equal(file.url, `${baseUrl}/v1/files/job-1`);
    assert.equal(file.format, format);
    assert.match(file.fileName, new RegExp(`\\.${format}$`));
  }

  console.log('selfcheck: ok');
} finally {
  await new Promise((resolve) => server.close(resolve));
  await rm(path, { force: true });
}
