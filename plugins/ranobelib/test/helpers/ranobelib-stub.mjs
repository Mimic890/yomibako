import { createFetchStub, jsonResponse, binaryResponse } from './http.mjs';

/**
 * Routes the fixtures the way the real API does. `chapterByKey` lets a test replace one chapter
 * response without rewriting the whole router.
 */
export function createRanobeLibFixtureFetch({
  search,
  title,
  chapters,
  chapter,
  cover,
  image,
  chapterByKey = () => chapter,
}) {
  return createFetchStub(async (url) => {
    if (url.hostname === 'api.cdnlibs.org') {
      if (url.pathname === '/api/manga/') return jsonResponse(search);

      const titleMatch = /^\/api\/manga\/([^/]+)(\/chapters|\/chapter)?$/.exec(url.pathname);
      if (titleMatch) {
        const slug = decodeURIComponent(titleMatch[1]);
        const suffix = titleMatch[2] ?? '';
        if (slug !== '101--test-novel') return undefined;
        if (suffix === '/chapters') return jsonResponse(chapters);
        if (suffix === '/chapter') {
          const key = `${url.searchParams.get('volume')}:${url.searchParams.get('number')}:${url.searchParams.get('branch_id') ?? 'null'}`;
          const result = chapterByKey(key, url);
          if (result === null) return undefined;
          return result?.status || result?.json || result?.bytes ? result : jsonResponse(result);
        }
        return jsonResponse(title);
      }
      return undefined;
    }

    if (url.hostname === 'cdn.example.invalid') {
      if (url.pathname === '/covers/101.png' || url.pathname === '/covers/101-thumb.png') {
        return binaryResponse(cover, { mediaType: 'image/png' });
      }
      if (url.pathname === '/chapters/img-1.png') {
        return binaryResponse(image, { mediaType: 'image/png' });
      }
    }
    return undefined;
  });
}
