/**
 * Yomibako configuration: a JSON file, with a few environment overrides for container setups.
 * Everything optional has a working default; running with no config file at all is supported.
 * The Node version keeps the original RUNTIME_PROXY_* env names for compatibility.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const DEFAULTS = {
  host: '127.0.0.1',
  port: 7345,
  token: '',
  fileTtlMinutes: 60,
  mihon: {
    enabled: true,
    container: 'miwayomi',
    baseUrl: 'http://127.0.0.1:4567',
    startTimeoutMs: 30000,
    idleMinutes: 10,
    imageConcurrency: 4,
    maxChapters: 0,
  },
  lnreader: {
    enabled: true,
    command: 'lnreader',
    home: '',
    concurrency: 2,
  },
};

export function loadConfig(path = process.env.RUNTIME_PROXY_CONFIG || fileURLToPath(new URL('./config.json', import.meta.url))) {
  let file = {};
  try {
    file = JSON.parse(readFileSync(path, 'utf8'));
  } catch (error) {
    if (error.code !== 'ENOENT') throw new Error(`Cannot read ${path}: ${error.message}`);
  }

  const config = {
    ...DEFAULTS,
    ...file,
    mihon: { ...DEFAULTS.mihon, ...file.mihon },
    lnreader: { ...DEFAULTS.lnreader, ...file.lnreader },
  };

  if (process.env.RUNTIME_PROXY_HOST) config.host = process.env.RUNTIME_PROXY_HOST;
  if (process.env.RUNTIME_PROXY_PORT) config.port = Number(process.env.RUNTIME_PROXY_PORT);
  if (process.env.RUNTIME_PROXY_TOKEN) config.token = process.env.RUNTIME_PROXY_TOKEN;
  if (process.env.MIWAYOMI_URL) config.mihon.baseUrl = process.env.MIWAYOMI_URL;

  config.host = String(config.host);
  config.port = Number(config.port);
  if (!Number.isInteger(config.port) || config.port < 1 || config.port > 65535) {
    throw new Error(`Invalid proxy port: ${config.port}`);
  }
  return config;
}
