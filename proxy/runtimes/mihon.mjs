/**
 * Mihon runtime: miwayomi (or any API-compatible server) started on demand, then stopped after idle.
 *
 * The proxy only needs miwayomi's REST API, so Suwayomi or another compatible server can replace it
 * by changing `mihon.baseUrl`; `mihon.container` is the Docker container to start and stop, and may
 * be left empty when the runtime is managed elsewhere.
 */
import { execFile } from 'node:child_process';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { ZipWriter } from '../zip.mjs';
import { sanitizeName } from '../util.mjs';

const exec = promisify(execFile);

export function createMihon(cfg = {}) {
  const base = () => String(cfg.baseUrl || '').replace(/\/+$/, '');
  const imageConcurrency = Math.max(1, Number(cfg.imageConcurrency) || 4);
  const maxChapters = Math.max(0, Number(cfg.maxChapters) || 0);
  const idleMs = Math.max(1, Number(cfg.idleMinutes) || 10) * 60_000;
  let lastUsed = Date.now();
  let active = 0;
  let sources = null;

  async function containerRunning() {
    try {
      const { stdout } = await exec('docker', ['inspect', '-f', '{{.State.Running}}', cfg.container], { timeout: 10_000 });
      return stdout.trim() === 'true';
    } catch {
      return false;
    }
  }

  async function waitForHealth(deadline) {
    let lastError;
    while (Date.now() < deadline) {
      try {
        const response = await fetch(`${base()}/api/v1/health`, { signal: AbortSignal.timeout(5000) });
        if (response.ok) return;
        lastError = new Error(`miwayomi answered ${response.status}`);
      } catch (error) {
        lastError = error;
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    throw new Error(`miwayomi did not become healthy in time: ${lastError?.message ?? 'no response'}`);
  }

  async function ensureRunning() {
    lastUsed = Date.now();
    if (cfg.container) {
      if (!(await containerRunning())) {
        try {
          await exec('docker', ['start', cfg.container], { timeout: 30_000 });
        } catch (error) {
          throw new Error(`Cannot start container "${cfg.container}": ${error.stderr || error.message}`);
        }
      }
    }
    await waitForHealth(Date.now() + (Number(cfg.startTimeoutMs) || 30_000));
  }

  async function withRuntime(fn) {
    active++;
    lastUsed = Date.now();
    try {
      await ensureRunning();
      return await fn();
    } finally {
      active--;
      lastUsed = Date.now();
    }
  }

  async function getJson(path) {
    const response = await fetch(`${base()}${path}`, { signal: AbortSignal.timeout(60_000) });
    if (!response.ok) throw new Error(`miwayomi ${path} answered ${response.status}`);
    return response.json();
  }

  async function getBuffer(url) {
    const response = await fetch(url, { signal: AbortSignal.timeout(120_000) });
    if (!response.ok) throw new Error(`Image answered ${response.status}`);
    return { data: Buffer.from(await response.arrayBuffer()), type: response.headers.get('content-type') || '' };
  }

  function findSource(sourceName) {
    if (!sources) return null;
    const wanted = String(sourceName ?? '').trim().toLowerCase();
    return sources.find((source) => String(source.id) === wanted || source.name.toLowerCase() === wanted) ?? null;
  }

  async function sourceId(sourceName) {
    sources ??= (await getJson('/api/v1/sources')).manga ?? [];
    const source = findSource(sourceName);
    if (!source) {
      const known = sources.slice(0, 12).map((item) => item.name).join(', ');
      throw new Error(`No miwayomi manga source named "${sourceName}". Loaded: ${known || 'none'}`);
    }
    return source.id;
  }

  return {
    enabled: cfg.enabled !== false,

    async health() {
      return {
        enabled: cfg.enabled !== false,
        container: cfg.container || null,
        running: cfg.container ? await containerRunning() : null,
      };
    },

    async search({ source, title, author, limit = 10 }) {
      return withRuntime(async () => {
        const id = await sourceId(source);
        const text = [title, author].filter(Boolean).join(' ');
        const page = await getJson(`/api/v1/manga/${id}/search?query=${encodeURIComponent(text)}&page=1`);
        const releases = (page.mangas ?? []).slice(0, Math.max(1, Math.min(Number(limit) || 10, 50)));
        return releases.map((manga) => ({
          guid: manga.url,
          title: manga.title,
          author: manga.author || null,
          format: 'cbz',
          infoUrl: null,
        }));
      });
    },

    /** Fetch every chapter, pack the pages into one CBZ. Called inside a job, so it may take minutes. */
    async grab({ source, guid, title }, outDir, signal) {
      return withRuntime(async () => {
        const outPath = join(outDir, 'download.cbz');
        const id = await sourceId(source);
        const query = encodeURIComponent(guid);
        const chapters = (await getJson(`/api/v1/manga/${id}/chapters?url=${query}`)).chapters ?? [];
        chapters.sort((a, b) => a.chapter_number - b.chapter_number || a.date_upload - b.date_upload);
        const wanted = maxChapters > 0 ? chapters.slice(0, maxChapters) : chapters;
        if (wanted.length === 0) throw new Error(`"${title}" has no chapters`);

        const zip = await new ZipWriter(outPath).open();
        let entry = 0;
        try {
          for (const [chapterIndex, chapter] of wanted.entries()) {
            if (signal?.aborted) throw new Error('Cancelled');
            const pages = (await getJson(`/api/v1/manga/${id}/pages?url=${encodeURIComponent(chapter.url)}`)).pages ?? [];
            const images = pages.map((page) => page.imageUrl || page.url).filter(Boolean);
            for (let start = 0; start < images.length; start += imageConcurrency) {
              const batch = images.slice(start, start + imageConcurrency);
              const downloads = await Promise.all(batch.map((image) => getBuffer(
                `${base()}/api/v1/proxy?sourceId=${id}&url=${encodeURIComponent(image)}`,
              )));
              for (const [index, { data, type }] of downloads.entries()) {
                entry++;
                const folder = `ch-${String(chapterIndex + 1).padStart(4, '0')}`;
                await zip.add(`${folder}/${String(entry).padStart(4, '0')}.${extensionOf(type, batch[index])}`, data);
              }
            }
          }
          await zip.close();
        } catch (error) {
          await zip.abort();
          throw error;
        }
        return { filePath: outPath, fileName: `${sanitizeName(title)}.cbz`, format: 'cbz' };
      });
    },

    async stopIfIdle() {
      if (!cfg.container || active > 0 || Date.now() - lastUsed < idleMs) return false;
      if (!(await containerRunning())) return false;
      try {
        await exec('docker', ['stop', '-t', '10', cfg.container], { timeout: 30_000 });
        return true;
      } catch {
        return false;
      }
    },
  };
}

function extensionOf(contentType, url) {
  const type = String(contentType).toLowerCase();
  if (type.includes('png')) return 'png';
  if (type.includes('webp')) return 'webp';
  if (type.includes('gif')) return 'gif';
  if (type.includes('avif')) return 'avif';
  if (type.includes('jpeg') || type.includes('jpg')) return 'jpg';
  const sample = String(url ?? '').toLowerCase();
  const match = /\.(jpe?g|png|webp|gif|avif)(?:\?|$)/.exec(sample);
  return match ? (match[1] === 'jpeg' ? 'jpg' : match[1]) : 'jpg';
}
