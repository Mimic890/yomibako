/**
 * HTTP client for RanobeLib's public API.
 *
 * Only the endpoints the module needs are implemented. Requests are serialised and spaced by
 * `minRequestIntervalMs` because the API advertises a 100-request window; the client does not retry
 * failures, so a throttled or broken source is reported rather than hidden behind repeated calls.
 */

import { RanobeLibError } from './errors.mjs';
import { detectImageMediaType, normalizeMediaType } from './media.mjs';
import { normalizeChapterContent, normalizeChapters, normalizeSearch, normalizeTitle } from './normalize.mjs';

export const DEFAULT_API_BASE = 'https://api.cdnlibs.org/api';
export const DEFAULT_SITE_BASE = 'https://ranobelib.me';
export const DEFAULT_SITE_ID = 3;
export const DEFAULT_TIMEOUT_MS = 25_000;
/** 100 requests per minute, with a little headroom. */
export const DEFAULT_MIN_REQUEST_INTERVAL_MS = 600;
const DEFAULT_MAX_IMAGE_BYTES = 30 * 1024 * 1024;

function sleep(ms, signal) {
  if (ms <= 0) return Promise.resolve();
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

function requestSignal(timeoutMs, signal) {
  const timeout = AbortSignal.timeout(timeoutMs);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

function retryAfter(response) {
  const raw = response.headers.get('retry-after');
  if (!raw) return null;
  const seconds = Number(raw);
  if (Number.isFinite(seconds)) return seconds;
  const date = Date.parse(raw);
  return Number.isFinite(date) ? Math.max(0, Math.ceil((date - Date.now()) / 1000)) : null;
}

export class RanobeLibClient {
  #fetch;
  #sleep;
  #now;
  #queue = Promise.resolve();
  #lastRequestAt = -Infinity;

  constructor({
    fetchImpl = globalThis.fetch,
    baseUrl = DEFAULT_API_BASE,
    siteUrl = DEFAULT_SITE_BASE,
    siteId = DEFAULT_SITE_ID,
    userAgent = 'Yomibako-RanobeLib/0.1',
    timeoutMs = DEFAULT_TIMEOUT_MS,
    minRequestIntervalMs = DEFAULT_MIN_REQUEST_INTERVAL_MS,
    maxImageBytes = DEFAULT_MAX_IMAGE_BYTES,
    sleepImpl = sleep,
    now = () => Date.now(),
  } = {}) {
    if (typeof fetchImpl !== 'function') throw new TypeError('fetchImpl must be a function');
    this.#fetch = fetchImpl;
    this.#sleep = sleepImpl;
    this.#now = now;
    this.baseUrl = baseUrl.replace(/\/+$/, '');
    this.siteUrl = siteUrl.replace(/\/+$/, '');
    this.siteId = siteId;
    this.userAgent = userAgent;
    this.timeoutMs = timeoutMs;
    this.minRequestIntervalMs = Math.max(0, minRequestIntervalMs);
    this.maxImageBytes = maxImageBytes;
  }

  bookUrl(slug) {
    return `${this.siteUrl}/ru/book/${encodeURIComponent(slug)}`;
  }

  #headers(kind) {
    const accept =
      kind === 'image' ? 'image/webp,image/png,image/jpeg,image/gif,image/svg+xml,image/*;q=0.8,*/*;q=0.1' : 'application/json';
    return {
      Accept: accept,
      Referer: `${this.siteUrl}/`,
      Origin: `${this.siteUrl}/`,
      'Site-Id': String(this.siteId),
      'User-Agent': this.userAgent,
    };
  }

  async #throttle(signal) {
    const previous = this.#queue;
    let release;
    this.#queue = new Promise((resolve) => {
      release = resolve;
    });
    await previous;
    try {
      const waitMs = this.#lastRequestAt + this.minRequestIntervalMs - this.#now();
      if (waitMs > 0) await this.#sleep(waitMs, signal);
      this.#lastRequestAt = this.#now();
    } finally {
      release();
    }
  }

  #url(path, params = null) {
    const url = new URL(`${this.baseUrl}${path}`);
    for (const [key, value] of Object.entries(params ?? {})) {
      if (value !== null && value !== undefined && value !== '') url.searchParams.set(key, String(value));
    }
    return url;
  }

  async #request(url, { signal, kind = 'json' } = {}) {
    await this.#throttle(signal);
    let response;
    try {
      response = await this.#fetch(url.href, {
        method: 'GET',
        headers: this.#headers(kind),
        redirect: 'follow',
        signal: requestSignal(this.timeoutMs, signal),
      });
    } catch (error) {
      if (signal?.aborted) {
        throw new RanobeLibError('RanobeLib request was aborted', { code: 'aborted', url: url.href, cause: error });
      }
      if (error?.name === 'TimeoutError' || error?.name === 'AbortError') {
        throw new RanobeLibError('RanobeLib did not answer in time', { code: 'timeout', url: url.href, cause: error });
      }
      throw new RanobeLibError('RanobeLib could not be reached', { code: 'network', url: url.href, cause: error });
    }

    if (!response.ok) {
      const body = await response.text().catch(() => '');
      const code = response.status === 404 ? 'notFound' : response.status === 429 ? 'throttled' : 'http';
      throw new RanobeLibError(`RanobeLib answered HTTP ${response.status}`, {
        code,
        status: response.status,
        url: url.href,
        retryAfter: retryAfter(response),
        cause: body ? new Error(body.slice(0, 500)) : null,
      });
    }
    return response;
  }

  async #requestJson(url, options) {
    const response = await this.#request(url, { ...options, kind: 'json' });
    try {
      return await response.json();
    } catch (error) {
      throw new RanobeLibError('RanobeLib returned invalid JSON', { code: 'parse', url: url.href, cause: error });
    }
  }

  async search({ query, page = 1, limit = null, signal } = {}) {
    if (typeof query !== 'string' || query.trim() === '') {
      throw new RanobeLibError('A non-empty search query is required', { code: 'invalid' });
    }
    const pageNumber = Number(page);
    if (!Number.isInteger(pageNumber) || pageNumber < 1) {
      throw new RanobeLibError('Search page must be a positive integer', { code: 'invalid' });
    }
    const url = this.#url('/manga/', {
      'site_id[0]': this.siteId,
      q: query.trim(),
      page: pageNumber,
    });
    const result = normalizeSearch(await this.#requestJson(url, { signal }), { page: pageNumber, siteUrl: this.siteUrl });
    if (limit != null) result.items = result.items.slice(0, Math.max(0, Number(limit) || 0));
    return result;
  }

  async getTitle(slug, { signal } = {}) {
    const safeSlug = this.#slug(slug);
    const url = this.#url(`/manga/${encodeURIComponent(safeSlug)}`);
    // URLSearchParams cannot express repeated `fields[]` with set(); append them explicitly.
    for (const field of ['summary', 'genres', 'tags', 'teams', 'authors', 'status_id', 'artists']) {
      url.searchParams.append('fields[]', field);
    }
    return normalizeTitle(await this.#requestJson(url, { signal }), { siteUrl: this.siteUrl });
  }

  async getChapters(slug, { signal } = {}) {
    const safeSlug = this.#slug(slug);
    const url = this.#url(`/manga/${encodeURIComponent(safeSlug)}/chapters`);
    return normalizeChapters(await this.#requestJson(url, { signal }));
  }

  async getChapter(slug, { volume = null, number = null, branchId = null, title = null, signal } = {}) {
    const safeSlug = this.#slug(slug);
    if (number == null || String(number) === '') {
      throw new RanobeLibError('A chapter number is required', { code: 'invalid' });
    }
    const url = this.#url(`/manga/${encodeURIComponent(safeSlug)}/chapter`, {
      number,
      volume,
      branch_id: branchId == null || branchId === 0 ? null : branchId,
    });
    const result = normalizeChapterContent(await this.#requestJson(url, { signal }), { title });
    return { ...result, volume, number, branchId };
  }

  async fetchImage(url, { signal, maxBytes = this.maxImageBytes } = {}) {
    let parsed;
    try {
      parsed = new URL(String(url));
    } catch (error) {
      throw new RanobeLibError('Image URL is invalid', { code: 'invalid', cause: error });
    }
    if (!/^https?:$/.test(parsed.protocol)) {
      throw new RanobeLibError('Image URL must use http or https', { code: 'invalid', url: parsed.href });
    }

    const response = await this.#request(parsed, { signal, kind: 'image' });
    const declaredLength = Number(response.headers.get('content-length'));
    if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
      throw new RanobeLibError('RanobeLib image is too large', { code: 'tooLarge', url: parsed.href });
    }

    const bytes = new Uint8Array(await response.arrayBuffer());
    if (bytes.byteLength === 0) throw new RanobeLibError('RanobeLib returned an empty image', { code: 'empty', url: parsed.href });
    if (bytes.byteLength > maxBytes) throw new RanobeLibError('RanobeLib image is too large', { code: 'tooLarge', url: parsed.href });

    const mediaType = detectImageMediaType(bytes, normalizeMediaType(response.headers.get('content-type')));
    if (!mediaType || !mediaType.startsWith('image/')) {
      throw new RanobeLibError('RanobeLib returned something that is not an image', { code: 'parse', url: parsed.href });
    }
    return { bytes, mediaType, url: parsed.href };
  }

  #slug(slug) {
    const value = typeof slug === 'string' ? slug.trim() : '';
    if (!value || value.includes('/') || value.includes('?') || value.includes('#') || /\s/.test(value)) {
      throw new RanobeLibError('A valid RanobeLib slug is required', { code: 'invalid' });
    }
    return value;
  }
}
