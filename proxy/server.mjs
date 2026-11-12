#!/usr/bin/env node
/**
 * Yomibako: local runtime proxy for BookOrbit.
 *
 * Always running, tiny, and the only thing the BookOrbit plugins talk to. It keeps the mapping
 * "source → runtime", starts the Mihon runtime when a request needs it, forwards the request, and
 * lets the idle timer stop it again. LNReader needs no daemon and is spawned per request.
 *
 * Endpoints:
 *   GET  /health
 *   POST /v1/search  {runtime, source, title, author, limit}
 *   POST /v1/grab    {runtime, source, guid, title}
 *   GET  /v1/files/<id>
 */
import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import http from 'node:http';
import { loadConfig } from './config.mjs';
import { Jobs } from './jobs.mjs';
import { createLnReader } from './runtimes/lnreader.mjs';
import { createMihon } from './runtimes/mihon.mjs';

const MAX_BODY_BYTES = 64 * 1024;

const config = loadConfig();
const runtimes = {
  mihon: createMihon(config.mihon),
  lnreader: createLnReader(config.lnreader),
};
const jobs = new Jobs(runtimes, config);
await jobs.init();

const server = http.createServer(async (request, response) => {
  try {
    const url = new URL(request.url, 'http://localhost');
    if (config.token && !authorized(request)) return sendJson(response, 401, { error: 'Unauthorized' });

    if (request.method === 'GET' && url.pathname === '/health') {
      const health = Object.fromEntries(
        await Promise.all(Object.entries(runtimes).map(async ([name, runtime]) => [name, await runtime.health()])),
      );
      return sendJson(response, 200, { ok: true, runtimes: health });
    }

    if (request.method === 'POST' && url.pathname === '/v1/search') {
      const body = await readJson(request);
      const runtime = pickRuntime(body.runtime);
      const releases = await runtime.search({
        source: requireString(body.source, 'source'),
        title: requireString(body.title, 'title'),
        author: typeof body.author === 'string' ? body.author : null,
        limit: body.limit,
      });
      return sendJson(response, 200, { releases });
    }

    if (request.method === 'POST' && url.pathname === '/v1/grab') {
      const body = await readJson(request);
      const runtimeName = requireString(body.runtime, 'runtime');
      pickRuntime(runtimeName);
      const job = jobs.start(runtimeName, {
        source: requireString(body.source, 'source'),
        guid: requireString(body.guid, 'guid'),
        title: requireString(body.title, 'title'),
      });
      return sendJson(response, 202, {
        id: job.id,
        fileName: job.fileName,
        format: job.format,
        url: `/v1/files/${job.id}`,
      });
    }

    if (request.method === 'GET' && url.pathname.startsWith('/v1/files/')) {
      const id = url.pathname.slice('/v1/files/'.length);
      const job = await jobs.wait(id);
      if (!job) return sendJson(response, 404, { error: 'Unknown download' });
      if (job.status === 'error') return sendJson(response, 502, { error: job.error });
      return sendFile(response, job);
    }

    return sendJson(response, 404, { error: 'Not found' });
  } catch (error) {
    const status = error.statusCode ?? 502;
    if (status >= 500) console.error(error);
    return sendJson(response, status, { error: error instanceof Error ? error.message : String(error) });
  }
});

server.listen(config.port, config.host, () => {
  console.log(`Yomibako on http://${config.host}:${config.port}`);
  console.log(`  mihon:    ${config.mihon.enabled ? `${config.mihon.baseUrl} (container ${config.mihon.container || 'external'}, idle ${config.mihon.idleMinutes}m)` : 'disabled'}`);
  console.log(`  lnreader: ${config.lnreader.enabled ? `${config.lnreader.command}${config.lnreader.home ? ` (home ${config.lnreader.home})` : ''}` : 'disabled'}`);
});

const idleTimer = setInterval(() => {
  for (const runtime of Object.values(runtimes)) Promise.resolve(runtime.stopIfIdle()).catch((error) => console.error(error));
}, 60_000);
idleTimer.unref();

const sweepTimer = setInterval(() => jobs.sweep(), 10 * 60_000);
sweepTimer.unref();

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 5000).unref();
  });
}

function authorized(request) {
  const header = request.headers.authorization ?? '';
  const token = header.startsWith('Bearer ') ? header.slice(7) : request.headers['x-proxy-token'];
  return token === config.token;
}

function pickRuntime(name) {
  if (typeof name !== 'string' || !runtimes[name]) throw httpError(400, `Unknown runtime "${name}"`);
  return runtimes[name];
}

function requireString(value, field) {
  if (typeof value !== 'string' || !value.trim()) throw httpError(400, `"${field}" is required`);
  return value.trim();
}

async function readJson(request) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > MAX_BODY_BYTES) throw httpError(413, 'Request body is too large');
    chunks.push(chunk);
  }
  if (chunks.length === 0) return {};
  try {
    return JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } catch {
    throw httpError(400, 'Request body is not JSON');
  }
}

function sendJson(response, status, value) {
  const body = Buffer.from(JSON.stringify(value));
  response.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': body.length,
  });
  response.end(body);
}

async function sendFile(response, job) {
  if (!job.filePath) return sendJson(response, 502, { error: 'Download has no file' });
  const info = await stat(job.filePath).catch(() => null);
  if (!info?.isFile()) return sendJson(response, 502, { error: 'Downloaded file disappeared' });
  response.writeHead(200, {
    'content-type': job.format === 'cbz' ? 'application/vnd.comicbook+zip' : 'application/epub+zip',
    'content-length': info.size,
    'content-disposition': contentDisposition(job.fileName),
  });
  createReadStream(job.filePath)
    .on('error', () => response.destroy())
    .pipe(response);
}

function contentDisposition(name) {
  const fallback = name.replace(/[^\x20-\x7e]/g, '_').replace(/["\\]/g, '_');
  return `attachment; filename="${fallback}"; filename*=UTF-8''${encodeURIComponent(name)}`;
}

function httpError(statusCode, message) {
  return Object.assign(new Error(message), { statusCode });
}
