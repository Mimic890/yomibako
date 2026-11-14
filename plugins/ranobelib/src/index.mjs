/**
 * RanobeLib module.
 *
 * This is a library, not a BookOrbit indexer. It deliberately has no default export and no
 * `apiVersion`/`search`/`resolveFile` plugin surface: BookOrbit's plugin loader must not mistake it
 * for a working indexer while `resolveFile` still accepts only a public URL.
 *
 * The full book path is implemented here: search, title card, chapters, chapter text, cover,
 * images and EPUB assembly. A future plugin layer can map these models to the BookOrbit contract.
 */

import { RanobeLibClient } from './client.mjs';
import { documentToHtml } from './document.mjs';
import { buildEpub, bookFileName } from './epub.mjs';
import { RanobeLibError } from './errors.mjs';
import { imageExtension } from './media.mjs';

export { RanobeLibClient } from './client.mjs';
export { buildEpub, bookFileName, EPUB_STYLESHEET } from './epub.mjs';
export { RanobeLibError } from './errors.mjs';
export { documentToHtml, documentToText, collectImageAttachments } from './document.mjs';
export { normalizeSearch, normalizeTitle, normalizeChapters, normalizeChapterContent } from './normalize.mjs';

function branchMatches(branch, selector) {
  return String(branch.branchId) === String(selector) || String(branch.id) === String(selector);
}

/**
 * Pick one branch for a chapter.
 *
 * `auto` prefers the main branch (no `branch_id`) and otherwise takes the first branch the API
 * returned. A function receives the chapter and returns a branch, so a caller can implement its own
 * preference for a translation team.
 */
export function chooseBranch(chapter, selector = 'auto') {
  const branches = Array.isArray(chapter?.branches) ? chapter.branches : [];
  if (branches.length === 0) return null;
  if (typeof selector === 'function') {
    const chosen = selector(chapter);
    if (chosen == null) return null;
    if (typeof chosen === 'object') return branches.includes(chosen) ? chosen : null;
    return branches.find((branch) => branchMatches(branch, chosen)) ?? null;
  }
  if (selector === 'auto' || selector == null) {
    return branches.find((branch) => branch.branchId == null || branch.branchId === 0) ?? branches[0];
  }
  if (selector === 'first') return branches[0];
  return branches.find((branch) => branchMatches(branch, selector)) ?? null;
}

function chapterLabel(chapter) {
  return chapter.displayTitle ?? `Том ${chapter.volume ?? '?'} Глава ${chapter.number ?? '?'}`;
}

export class RanobeLib {
  constructor(options = {}) {
    this.client = new RanobeLibClient(options);
  }

  search(query, options = {}) {
    return this.client.search({ query, ...options });
  }

  getTitle(slug, options = {}) {
    return this.client.getTitle(slug, options);
  }

  getChapters(slug, options = {}) {
    return this.client.getChapters(slug, options);
  }

  /**
   * Fetch one chapter. `chapter` may be a model from `getChapters()` or a plain
   * `{ volume, number, branchId }` object.
   */
  getChapter(slug, chapter, { branch = 'auto', signal } = {}) {
    if (!chapter || typeof chapter !== 'object') throw new TypeError('chapter is required');
    const selected = Array.isArray(chapter.branches) ? chooseBranch(chapter, branch) : chapter;
    if (Array.isArray(chapter.branches) && !selected) {
      throw new RanobeLibError(`No branch matches ${String(branch)} for ${chapterLabel(chapter)}`, { code: 'notFound' });
    }
    return this.client.getChapter(slug, {
      volume: chapter.volume ?? null,
      number: chapter.number ?? null,
      branchId: selected?.branchId ?? chapter.branchId ?? null,
      title: chapter.displayTitle ?? null,
      signal,
    });
  }

  async getCover(slugOrTitle, { signal } = {}) {
    const title = typeof slugOrTitle === 'string' ? await this.getTitle(slugOrTitle, { signal }) : slugOrTitle;
    if (!title?.cover?.url) return null;
    const image = await this.client.fetchImage(title.cover.url, { signal });
    return { ...image, fileName: `cover.${imageExtension(image.mediaType) ?? 'jpg'}` };
  }

  /**
   * Fetch everything and return a ready EPUB.
   *
   * The source is read sequentially and through one rate-limited client, so building a book does
   * not turn into a burst of parallel chapter requests. A chapter that genuinely returns no text is
   * skipped with a warning; a network or API failure aborts unless `continueOnError` is true.
   */
  async buildBook(slugOrTitle, {
    branch = 'auto',
    maxChapters = 0,
    includeImages = true,
    continueOnError = false,
    language = 'ru',
    modifiedAt = new Date(),
    signal,
    onProgress = null,
  } = {}) {
    const progress = (event) => {
      if (typeof onProgress !== 'function') return;
      try {
        onProgress(event);
      } catch {
        // A reporting callback must never break the book build.
      }
    };

    progress({ stage: 'title' });
    const title = typeof slugOrTitle === 'string' ? await this.getTitle(slugOrTitle, { signal }) : slugOrTitle;
    if (!title?.slug) throw new RanobeLibError('A title slug is required', { code: 'invalid' });

    progress({ stage: 'chapters' });
    let chapters = await this.getChapters(title.slug, { signal });
    if (maxChapters > 0) chapters = chapters.slice(0, maxChapters);
    if (chapters.length === 0) throw new RanobeLibError('RanobeLib returned no chapters', { code: 'empty' });

    const warnings = [];
    const chapterDocs = [];
    const images = [];
    const total = chapters.length;

    for (const [index, chapter] of chapters.entries()) {
      const selected = chooseBranch(chapter, branch);
      if (!selected) {
        warnings.push(`${chapterLabel(chapter)}: нет подходящей ветки перевода`);
        continue;
      }

      progress({ stage: 'chapter', current: index + 1, total, message: chapterLabel(chapter) });
      let content;
      try {
        content = await this.client.getChapter(title.slug, {
          volume: chapter.volume ?? null,
          number: chapter.number ?? null,
          branchId: selected.branchId ?? null,
          title: chapterLabel(chapter),
          signal,
        });
      } catch (error) {
        if (!continueOnError) throw error;
        warnings.push(`${chapterLabel(chapter)}: ${error.message}`);
        continue;
      }

      if (!content.text && !content.html) {
        warnings.push(`${chapterLabel(chapter)}: пустая глава`);
        continue;
      }

      const imageHrefs = new Map();
      if (includeImages) {
        let imageNumber = 0;
        for (const image of content.images) {
          imageNumber += 1;
          try {
            const fetched = await this.client.fetchImage(image.url, { signal });
            const extension = imageExtension(fetched.mediaType);
            if (!extension) {
              warnings.push(`${chapterLabel(chapter)}: формат изображения ${fetched.mediaType} не поддержан`);
              continue;
            }
            const fileName = `chapter-${String(index + 1).padStart(3, '0')}-${String(imageNumber).padStart(2, '0')}.${extension}`;
            images.push({ bytes: fetched.bytes, mediaType: fetched.mediaType, fileName });
            imageHrefs.set(image.url, `../images/${fileName}`);
          } catch (error) {
            warnings.push(`${chapterLabel(chapter)}: изображение ${image.url}: ${error.message}`);
          }
        }
      }

      const html = content.document
        ? documentToHtml(content.document, content.attachments, {
            imageResolver: ({ url }) => imageHrefs.get(url) ?? null,
          })
        : content.html;
      chapterDocs.push({ title: chapterLabel(chapter), html });
    }

    if (chapterDocs.length === 0) {
      throw new RanobeLibError('All chapters were empty or failed', { code: 'empty', cause: warnings.length ? new Error(warnings.join('; ')) : null });
    }

    let cover = null;
    if (title.cover?.url) {
      progress({ stage: 'cover' });
      try {
        cover = await this.getCover(title, { signal });
      } catch (error) {
        warnings.push(`Обложка: ${error.message}`);
      }
    }

    progress({ stage: 'epub' });
    const bytes = buildEpub({
      metadata: title,
      chapters: chapterDocs,
      cover,
      images,
      language,
      modifiedAt,
    });

    return {
      bytes,
      fileName: bookFileName(title.title, title.slug),
      title,
      chapters: chapterDocs.map((chapter) => chapter.title),
      warnings,
    };
  }
}

export default RanobeLib;
