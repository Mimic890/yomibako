/**
 * LNReader runtime: the official `@lnreader/cli`, spawned per request.
 *
 * The CLI is already short-lived and remembers its plugin index and chapter cache under `--home`,
 * so there is no daemon to start or stop here. That is also why the idle timer only applies to
 * Mihon: an idle Node process would cost memory for nothing.
 *
 * Credentials for individual sources are set once with `lnreader auth <plugin> --home <dir>`; this
 * proxy deliberately does not collect them.
 */
import { execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import { promisify } from 'node:util';

const exec = promisify(execFile);

export function createLnReader(cfg = {}) {
  const command = cfg.command || 'lnreader';
  const home = cfg.home || '';
  const limit = Math.max(1, Number(cfg.concurrency) || 2);
  let active = 0;
  const waiting = [];

  async function acquire() {
    if (active < limit) {
      active++;
      return;
    }
    await new Promise((resolve) => waiting.push(resolve));
    active++;
  }

  function release() {
    active--;
    waiting.shift()?.();
  }

  function argsFor(args) {
    return [...(home ? ['--home', home] : []), ...args, '--no-interactive'];
  }

  async function run(args, signal, timeout) {
    try {
      const { stdout } = await exec(command, argsFor(args), {
        timeout: timeout ?? 0,
        maxBuffer: 16 * 1024 * 1024,
        signal,
      });
      return stdout;
    } catch (error) {
      if (error.code === 'ENOENT') {
        throw new Error(`LNReader CLI not found ("${command}"). Install it with: npm i -g @lnreader/cli`);
      }
      const detail = String(error.stderr || error.stdout || error.message).trim().split('\n').slice(-4).join(' ');
      throw new Error(`lnreader ${args[0]} failed: ${detail}`);
    }
  }

  return {
    enabled: cfg.enabled !== false,

    async health() {
      try {
        const { stdout } = await exec(command, ['--version'], { timeout: 5000 });
        return { enabled: cfg.enabled !== false, command, available: true, version: stdout.trim() };
      } catch {
        return { enabled: cfg.enabled !== false, command, available: false };
      }
    },

    async search({ source, title, author, limit: requested }, signal) {
      if (!source) throw new Error('No LNReader plugin id configured');
      await acquire();
      try {
        const text = [title, author].filter(Boolean).join(' ');
        const stdout = await run(
          ['search', text, '--plugin', source, '--limit', String(Math.max(1, Math.min(Number(requested) || 10, 50))), '--json'],
          signal,
          120_000,
        );
        const parsed = JSON.parse(jsonSlice(stdout, '[', ']'));
        const entry = parsed.find((item) => String(item.plugin).toLowerCase() === String(source).toLowerCase());
        if (!entry) throw new Error(`No results array for plugin "${source}"`);
        if (entry.error) throw new Error(entry.error);
        return (entry.results ?? []).map((novel) => ({
          guid: novel.path,
          title: novel.name,
          author: null,
          format: 'epub',
          infoUrl: null,
        }));
      } finally {
        release();
      }
    },

    async grab({ source, guid, title }, outDir, signal) {
      if (!source) throw new Error('No LNReader plugin id configured');
      await acquire();
      try {
        const stdout = await run(['download', guid, '--plugin', source, '--out', outDir, '--json'], signal);
        const result = JSON.parse(jsonSlice(stdout, '{', '}'));
        const file = (result.files ?? [])[0];
        if (!file) {
          const failed = (result.failed ?? []).map((item) => item.error).filter(Boolean).slice(0, 3).join('; ');
          throw new Error(`lnreader produced no EPUB${failed ? `: ${failed}` : ''}`);
        }
        if (!existsSync(file)) throw new Error(`lnreader reported ${file}, but it does not exist`);
        return {
          filePath: file,
          fileName: file.split(/[\\/]/).pop() || `${title || 'book'}.epub`,
          format: 'epub',
        };
      } finally {
        release();
      }
    },

    stopIfIdle() {
      return false;
    },
  };
}

/** The CLI can print warnings around the JSON document; take the outermost JSON value. */
function jsonSlice(text, open, close) {
  const start = text.indexOf(open);
  const end = text.lastIndexOf(close);
  if (start === -1 || end <= start) throw new Error(`lnreader did not print JSON: ${text.slice(0, 200)}`);
  return text.slice(start, end + 1);
}
