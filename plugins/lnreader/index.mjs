/**
 * LNReader sources as a BookOrbit indexer plugin.
 *
 * Runs no parser of its own: every search and every download is one call to Yomibako,
 * which spawns `@lnreader/cli` per request and hands back a finished EPUB 3. One installed
 * BookOrbit indexer per LNReader plugin id, for example RLIB.
 *
 * A plugin runs inside the BookOrbit process with that process's access, so this is deliberately
 * one dependency-free file you can read start to finish.
 */
export default {
  apiVersion: 1,
  version: '0.1.0',
  type: 'lnreader-runtime',
  label: 'LNReader (Yomibako)',
  requiresCredential: false,
  credentialKind: null,
  mediaKinds: ['ebook'],
  supportsIsbnSearch: false,
  usesCategories: false,
  seedsBack: false,
  defaultBaseUrl: 'http://127.0.0.1:7345',
  baseUrlHint: 'Where Yomibako listens. Start Yomibako first.',
  settingsFields: [
    {
      key: 'source',
      type: 'string',
      label: 'LNReader plugin id',
      hint: 'The id from `lnreader plugins list`, for example RLIB. One installed indexer per plugin.',
      default: '',
    },
    {
      key: 'token',
      type: 'string',
      label: 'Yomibako token',
      hint: 'Leave empty unless Yomibako\'s config.json sets "token".',
      default: '',
    },
  ],

  async search(query, config, host) {
    const source = setting(config, 'source');
    if (!source) throw host.fail('error', 'Set the LNReader plugin id in this indexer\'s settings');

    const response = await call(config, host, 'POST', 'v1/search', {
      runtime: 'lnreader',
      source,
      title: query.title,
      author: query.author,
      limit: query.limit,
    });

    return (response.releases ?? []).map((release) => ({
      guid: release.guid,
      title: release.title,
      bookTitle: release.title,
      sizeBytes: null,
      // No swarm exists; null rather than zero, or the zero-seeder filter drops everything.
      seeders: null,
      leechers: null,
      format: 'epub',
      primaryFileCount: 1,
    }));
  },

  async test(config, host) {
    try {
      const health = await call(config, host, 'GET', 'health');
      if (!health.ok) return { success: false, error: 'The proxy answered, but is not ready' };
      const runtime = health.runtimes?.lnreader;
      if (runtime?.enabled === false) return { success: false, error: 'The LNReader runtime is disabled in proxy config' };
      if (runtime && runtime.available === false) return { success: false, error: `LNReader CLI not found ("${runtime.command}")` };
      return { success: true, indexerName: 'LNReader via Yomibako' };
    } catch (error) {
      return { success: false, error: error instanceof Error ? error.message : String(error) };
    }
  },

  async resolveFile(release, config, host) {
    const source = setting(config, 'source');
    if (!source) throw host.fail('error', 'Set the LNReader plugin id in this indexer\'s settings');

    const job = await call(config, host, 'POST', 'v1/grab', {
      runtime: 'lnreader',
      source,
      guid: release.guid,
      title: release.bookTitle || release.title,
    });

    return {
      // The proxy answers immediately and downloads while BookOrbit fetches this URL.
      url: endpoint(config, job.url),
      fileName: job.fileName,
      sizeBytes: null,
      format: job.format || 'epub',
    };
  },
};

function endpoint(config, path) {
  const base = String(config.baseUrl || 'http://127.0.0.1:7345').trim();
  return new URL(String(path).replace(/^\/+/, ''), base.endsWith('/') ? base : `${base}/`).href;
}

function setting(config, key) {
  const value = config.settings?.[key];
  return typeof value === 'string' ? value.trim() : '';
}

async function call(config, host, method, path, body) {
  const headers = { accept: 'application/json' };
  if (body) headers['content-type'] = 'application/json';
  const token = setting(config, 'token');
  if (token) headers.authorization = `Bearer ${token}`;

  let response;
  try {
    response = await host.fetch(endpoint(config, path), {
      method,
      headers,
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
  } catch (error) {
    const name = error instanceof Error ? error.name : '';
    if (name === 'AbortError' || name === 'TimeoutError') throw host.fail('timeout', 'Yomibako did not answer in time');
    throw host.fail('unreachable', `could not reach Yomibako: ${error instanceof Error ? error.message : String(error)}`);
  }

  const text = await response.text();
  if (response.status === 401 || response.status === 403) throw host.fail('unauthorized', 'Yomibako rejected the token');
  if (!response.ok) throw host.fail('error', messageOf(text, response.status));
  try {
    return JSON.parse(text);
  } catch {
    throw host.fail('error', 'Yomibako answered with something that is not JSON');
  }
}

function messageOf(text, status) {
  try {
    const parsed = JSON.parse(text);
    if (typeof parsed.error === 'string' && parsed.error) return parsed.error;
  } catch {
    // Not JSON; fall through to the generic message.
  }
  return `Yomibako answered ${status}`;
}
