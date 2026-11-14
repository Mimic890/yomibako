export function jsonResponse(data, { status = 200, headers = {} } = {}) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  });
}

export function binaryResponse(bytes, { status = 200, mediaType = 'application/octet-stream', headers = {} } = {}) {
  return new Response(bytes, {
    status,
    headers: { 'content-type': mediaType, ...headers },
  });
}

/**
 * A fetch double that records every request and lets the test decide the answer.
 * `handler(url, init, call)` may return a Response, `{ json, status? }`, `{ bytes, mediaType? }`,
 * or `undefined` for a 404.
 */
export function createFetchStub(handler) {
  const calls = [];
  const fetch = async (input, init = {}) => {
    const raw = typeof input === 'string' ? input : input?.url ?? String(input);
    const url = new URL(raw);
    const call = { url, init, href: url.href };
    calls.push(call);
    const result = await handler(url, init, call);
    if (result instanceof Response) return result;
    if (result === undefined || result === null) return jsonResponse({ error: 'not found' }, { status: 404 });
    if (result.bytes) return binaryResponse(result.bytes, { mediaType: result.mediaType, status: result.status });
    return jsonResponse(result.json, { status: result.status ?? 200, headers: result.headers });
  };
  return { fetch, calls };
}
