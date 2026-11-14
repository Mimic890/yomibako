/**
 * Mapping from RanobeLib's API shapes to the module's own models.
 *
 * The API is not stable enough to pass through untouched: fields are empty strings rather than
 * nulls, branches are nested one level deeper than chapters, and chapter order is not guaranteed.
 * Everything the module returns is normalised here so callers never have to know which of
 * `rus_name`, `name` or `eng_name` happened to be filled in.
 */

import { collectImageAttachments, documentToHtml, documentToText, plainTextToHtml } from './document.mjs';
import { RanobeLibError } from './errors.mjs';

function text(value) {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed === '' ? null : trimmed;
}

function firstText(...values) {
  for (const value of values) {
    const found = text(value);
    if (found) return found;
  }
  return null;
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === '') return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function uniqueStrings(values) {
  const seen = new Set();
  const result = [];
  for (const value of values) {
    const item = text(value);
    if (!item) continue;
    const key = item.toLocaleLowerCase('ru');
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(item);
  }
  return result;
}

function coverUrls(cover) {
  if (!cover || typeof cover !== 'object') {
    return { url: null, thumbnailUrl: null, mediumUrl: null };
  }
  return {
    url: firstText(cover.default, cover.md, cover.thumbnail),
    thumbnailUrl: firstText(cover.thumbnail, cover.default, cover.md),
    mediumUrl: firstText(cover.md, cover.default, cover.thumbnail),
  };
}

function namedList(values) {
  if (!Array.isArray(values)) return [];
  return uniqueStrings(values.map((value) => value?.name ?? value?.label ?? value));
}

function slugFrom(item) {
  return firstText(item?.slug_url, item?.slug) ?? (item?.id != null ? String(item.id) : null);
}

function bookUrl(siteUrl, slug) {
  if (!slug) return null;
  return `${siteUrl.replace(/\/+$/, '')}/ru/book/${encodeURIComponent(slug)}`;
}

/** One row of the search response. */
export function normalizeSearchItem(item, { siteUrl }) {
  const title = firstText(item?.rus_name, item?.name, item?.eng_name);
  const alternativeTitles = uniqueStrings([item?.eng_name, item?.name, item?.rus_name].filter((value) => value !== title));
  const slug = slugFrom(item);
  return {
    id: numberOrNull(item?.id),
    slug,
    title,
    alternativeTitles,
    cover: coverUrls(item?.cover),
    status: item?.status?.label ?? null,
    scanlateStatus: item?.scanlateStatus?.label ?? null,
    type: item?.type?.label ?? null,
    rating: numberOrNull(item?.rating?.average),
    votes: numberOrNull(item?.rating?.votes),
    releaseDate: text(item?.releaseDateString),
    ageRestriction: item?.ageRestriction?.label ?? null,
    isLicensed: item?.is_licensed === true,
    url: bookUrl(siteUrl, slug),
  };
}

export function normalizeSearch(json, { page = 1, siteUrl } = {}) {
  if (!json || !Array.isArray(json.data)) {
    throw new RanobeLibError('RanobeLib returned an unexpected search response', { code: 'parse' });
  }

  const items = json.data.map((item) => normalizeSearchItem(item, { siteUrl })).filter((item) => item.title && item.slug);
  const currentPage = numberOrNull(json.meta?.current_page) ?? page;
  const perPage = numberOrNull(json.meta?.per_page) ?? null;
  const hasNextPage = json.meta?.has_next_page === true || Boolean(json.links?.next);

  return {
    items,
    pagination: {
      page: currentPage,
      perPage,
      hasNextPage,
      nextPage: hasNextPage ? currentPage + 1 : null,
    },
  };
}

/** A full title card. */
export function normalizeTitle(json, { siteUrl } = {}) {
  const data = json?.data;
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new RanobeLibError('RanobeLib returned an unexpected title response', { code: 'parse' });
  }

  const title = firstText(data.rus_name, data.name, data.eng_name);
  if (!title) throw new RanobeLibError('RanobeLib returned a title without a name', { code: 'parse' });

  const slug = slugFrom(data);
  const alternativeTitles = uniqueStrings([data.eng_name, data.name, data.rus_name].filter((value) => value !== title));
  const description = documentToText(data.summary) || null;

  return {
    id: numberOrNull(data.id),
    slug,
    title,
    alternativeTitles,
    description,
    authors: namedList(data.authors),
    artists: namedList(data.artists),
    genres: namedList(data.genres),
    tags: namedList(data.tags),
    teams: namedList(data.teams),
    status: data.status?.label ?? null,
    scanlateStatus: data.scanlateStatus?.label ?? null,
    type: data.type?.label ?? null,
    rating: numberOrNull(data.rating?.average),
    votes: numberOrNull(data.rating?.votes),
    releaseDate: text(data.releaseDateString),
    ageRestriction: data.ageRestriction?.label ?? null,
    isLicensed: data.is_licensed === true,
    edition: text(data.edition),
    cover: coverUrls(data.cover),
    sourceUrl: bookUrl(siteUrl, slug),
  };
}

function chapterKey(chapter) {
  const volume = chapter.volume == null ? '' : String(chapter.volume);
  const number = chapter.number == null ? '' : String(chapter.number);
  if (volume || number) return `${volume}\u0000${number}`;
  return `index\u0000${chapter.index == null ? '' : String(chapter.index)}`;
}

function branchModel(branch) {
  return {
    id: numberOrNull(branch?.id),
    branchId: branch?.branch_id == null ? null : numberOrNull(branch.branch_id),
    createdAt: text(branch?.created_at),
    teamName: firstText(branch?.teams?.[0]?.name, branch?.user?.username),
  };
}

function displayTitle(chapter) {
  const parts = [];
  if (text(chapter.volume)) parts.push(`Том ${chapter.volume}`);
  if (text(chapter.number)) parts.push(`Глава ${chapter.number}`);
  const name = text(chapter.name);
  if (name) parts.push(name);
  return parts.join(' ') || `Глава ${chapter.index ?? '?'}`;
}

function sortValue(chapter) {
  const index = numberOrNull(chapter.index);
  if (index !== null) return index;
  const volume = numberOrNull(chapter.volume) ?? 0;
  const number = numberOrNull(chapter.number) ?? 0;
  return volume * 1_000_000 + number;
}

/**
 * Flattens branches into a chapter's variants, merges duplicate chapter rows, and sorts by the
 * source's own index rather than by response order.
 */
export function normalizeChapters(json) {
  if (!json || !Array.isArray(json.data)) {
    throw new RanobeLibError('RanobeLib returned an unexpected chapter list', { code: 'parse' });
  }

  const merged = new Map();
  for (const raw of json.data) {
    if (!raw || typeof raw !== 'object') continue;
    const chapter = {
      index: numberOrNull(raw.index),
      volume: text(raw.volume),
      number: text(raw.number),
      numberSecondary: text(raw.number_secondary),
      name: text(raw.name),
      branches: Array.isArray(raw.branches) && raw.branches.length > 0 ? raw.branches.map(branchModel) : [branchModel(null)],
    };
    chapter.displayTitle = displayTitle(chapter);
    const key = chapterKey(chapter);
    const existing = merged.get(key);
    if (!existing) {
      merged.set(key, chapter);
      continue;
    }
    const known = new Set(existing.branches.map((branch) => `${branch.branchId ?? 'null'}:${branch.id ?? 'null'}`));
    for (const branch of chapter.branches) {
      const branchKey = `${branch.branchId ?? 'null'}:${branch.id ?? 'null'}`;
      if (!known.has(branchKey)) {
        existing.branches.push(branch);
        known.add(branchKey);
      }
    }
  }

  return [...merged.values()].sort((left, right) => {
    const byIndex = sortValue(left) - sortValue(right);
    if (byIndex !== 0) return byIndex;
    return String(left.volume ?? '').localeCompare(String(right.volume ?? ''), 'ru', { numeric: true });
  });
}

/** Chapter text, with images resolved to their remote URLs for standalone use. */
export function normalizeChapterContent(json, { title = null } = {}) {
  const data = json?.data;
  if (!data || typeof data !== 'object') {
    throw new RanobeLibError('RanobeLib returned an unexpected chapter response', { code: 'parse' });
  }

  const content = data.content;
  const attachments = Array.isArray(data.attachments) ? data.attachments : [];
  const html = content == null ? '' : documentToHtml(content, attachments);
  const text = content == null ? '' : documentToText(content);

  return {
    title,
    html: html || (text ? plainTextToHtml(text) : ''),
    text: text.trim(),
    document: content && typeof content === 'object' ? content : null,
    attachments,
    images: collectImageAttachments(content, attachments),
  };
}
