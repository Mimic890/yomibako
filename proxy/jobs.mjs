/**
 * Downloads are long: a full manga can take minutes. `grab` returns a file URL immediately, the
 * work runs in the background, and the URL waits for the file when BookOrbit fetches it.
 */
import { randomUUID } from 'node:crypto';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { sanitizeName } from './util.mjs';

export class Jobs {
  #dir;
  #items = new Map();

  constructor(runtimes, config = {}) {
    this.runtimes = runtimes;
    this.ttlMs = Math.max(1, Number(config.fileTtlMinutes) || 60) * 60_000;
  }

  async init() {
    this.#dir = await mkdtemp(join(tmpdir(), 'yomibako-'));
  }

  start(runtimeName, params) {
    const runtime = this.runtimes[runtimeName];
    if (!runtime) throw new Error(`Unknown runtime "${runtimeName}"`);
    if (!runtime.enabled) throw new Error(`Runtime "${runtimeName}" is disabled`);

    const id = randomUUID();
    const format = runtimeName === 'mihon' ? 'cbz' : 'epub';
    const item = {
      id,
      status: 'running',
      fileName: `${sanitizeName(params.title)}.${format}`,
      format,
      filePath: null,
      error: null,
      createdAt: Date.now(),
    };
    item.done = this.#run(runtime, params, join(this.#dir, id), item);
    this.#items.set(id, item);
    return { id, fileName: item.fileName, format: item.format };
  }

  async #run(runtime, params, outDir, item) {
    try {
      await mkdir(outDir, { recursive: true });
      const result = await runtime.grab(params, outDir);
      item.filePath = result.filePath;
      item.status = 'done';
    } catch (error) {
      item.status = 'error';
      item.error = error instanceof Error ? error.message : String(error);
    }
  }

  async wait(id) {
    const item = this.#items.get(id);
    if (!item) return null;
    await item.done;
    return item;
  }

  sweep() {
    const cutoff = Date.now() - this.ttlMs;
    for (const [id, item] of this.#items) {
      if (item.status === 'running' || item.createdAt > cutoff) continue;
      this.#items.delete(id);
      rm(join(this.#dir, id), { recursive: true, force: true }).catch(() => {});
    }
  }
}
