/**
 * Mihon sources as a BookOrbit indexer plugin.
 *
 * Runs no parser of its own: every search and every grab is one call to Yomibako,
 * which starts miwayomi (or Suwayomi) on demand and builds a CBZ from the source's pages. Install
 * the search half of this pair on the proxy, and this file on BookOrbit.
 *
 * A plugin runs inside the BookOrbit process with that process's access, so this is deliberately
 * one dependency-free file you can read start to finish.
 *
 * BookOrbit's plugin API v1 asks only for ebooks and audiobooks, so the results are declared as
 * ebooks carrying a CBZ. Comics as a request medium were retired from the contract.
 */
export default {
  apiVersion: 1,
  version: '0.1.0',
  type: 'mihon-runtime',
  label: 'Mihon (Yomibako)',
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
      label: 'Source name in miwayomi',
      hint: 'Exactly as the runtime lists it, for example MangaLib or HentaiLib. One installed indexer per source.',
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
    if (!source) throw host.fail('error', 'Set the miwayomi source name in this indexer\'s settings');

    const response = await call(config, host, 'POST', 'v1/search', {
      runtime: 'mihon',
      source,
      title: query.title,
      author: query.author,
      limit: query.limit,
    });

    return (response.releases ?? []).map((release) => ({
      guid: release.guid,
      title: release.title,
      bookTitle: release.title,
      ...(release.author ? { author: release.author } : {}),
      // Building the archive happens on grab, so the size is not known yet.
      sizeBytes: null,
      // No swarm and no zero-seeder filter: null, never 0.
      seeders: null,
      leechers: null,
      format: 'cbz',
      primaryFileCount: 1,
    }));
  },

  async test(config, host) {
    try {
      const health = await call(config, host, 'GET', 'health');
      if (!health.ok) return { success: false, error: 'The proxy answered, but is not ready' };
      if (health.runtimes?.mihon?.enabled === false) return { success: false, error: 'The mihon runtime is disabled in proxy config' };
      return { success: true, indexerName: 'Mihon via Yomibako' };
    } catch (error) {
      return { success: false, error: error instanceof Error ? error.message : String(error) };
    }
  },

  async resolveFile(release, config, host) {
    const source = setting(config, 'source');
    if (!source) throw host.fail('error', 'Set the miwayomi source name in this indexer\'s settings');

    const job = await call(config, host, 'POST', 'v1/grab', {
      runtime: 'mihon',
      source,
      guid: release.guid,
      title: release.bookTitle || release.title,
    });

    return {
      // The proxy answers immediately and builds the CBZ while BookOrbit fetches this URL.
      url: endpoint(config, job.url),
      fileName: job.fileName,
      sizeBytes: null,
      format: job.format || 'cbz',
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
