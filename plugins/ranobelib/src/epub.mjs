/**
 * EPUB 3 writer.
 *
 * This is intentionally a small, dependency-free ZIP writer rather than a generic archive library:
 * an EPUB has one structural rule that most ZIP writers get wrong by default, namely that
 * `mimetype` must be the first entry and stored without compression. Writing the archive here makes
 * that rule explicit and testable.
 *
 * The builder accepts already-rendered XHTML fragments. Image hrefs inside those fragments must be
 * relative to `OEBPS/text/`, for example `../images/chapter-1.png`.
 */

import { deflateRawSync } from 'node:zlib';

import { escapeHtml } from './document.mjs';
import { detectImageMediaType, imageExtension, isSupportedImageMediaType, normalizeMediaType } from './media.mjs';

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let index = 0; index < 256; index += 1) {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) {
      value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    }
    table[index] = value >>> 0;
  }
  return table;
})();

function crc32(bytes) {
  let crc = 0xffffffff;
  for (const byte of bytes) crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

function dosDateTime(value) {
  const date = value instanceof Date && !Number.isNaN(value.valueOf()) ? value : new Date('1980-01-01T00:00:00Z');
  const year = Math.min(2107, Math.max(1980, date.getUTCFullYear()));
  const dosDate = ((year - 1980) << 9) | ((date.getUTCMonth() + 1) << 5) | date.getUTCDate();
  const dosTime = (date.getUTCHours() << 11) | (date.getUTCMinutes() << 5) | Math.floor(date.getUTCSeconds() / 2);
  return { dosDate, dosTime };
}

function writeUInt16(buffer, offset, value) {
  buffer.writeUInt16LE(value & 0xffff, offset);
}

function writeUInt32(buffer, offset, value) {
  buffer.writeUInt32LE(value >>> 0, offset);
}

/**
 * Build a ZIP archive. The caller's order is preserved, so the EPUB writer can put `mimetype`
 * first. `compress: false` is required for that first entry.
 */
export function createZip(entries, { modifiedAt = new Date() } = {}) {
  if (!Array.isArray(entries) || entries.length === 0) throw new TypeError('createZip needs at least one entry');
  const { dosDate, dosTime } = dosDateTime(modifiedAt);
  const localParts = [];
  const centralParts = [];
  const seen = new Set();
  let offset = 0;

  for (const entry of entries) {
    const path = String(entry.path);
    if (seen.has(path)) throw new TypeError(`Duplicate ZIP entry: ${path}`);
    seen.add(path);
    const name = Buffer.from(path, 'utf8');
    if (!name.length) throw new TypeError('ZIP entry path is empty');
    const raw = Buffer.isBuffer(entry.data) ? entry.data : Buffer.from(entry.data ?? []);
    const compress = entry.compress !== false;
    const data = compress ? deflateRawSync(raw, { level: 9 }) : raw;
    const method = compress ? 8 : 0;
    const crc = crc32(raw);
    const flags = 0x0800; // UTF-8 names

    const local = Buffer.alloc(30);
    writeUInt32(local, 0, 0x04034b50);
    writeUInt16(local, 4, 20);
    writeUInt16(local, 6, flags);
    writeUInt16(local, 8, method);
    writeUInt16(local, 10, dosTime);
    writeUInt16(local, 12, dosDate);
    writeUInt32(local, 14, crc);
    writeUInt32(local, 18, data.length);
    writeUInt32(local, 22, raw.length);
    writeUInt16(local, 26, name.length);
    writeUInt16(local, 28, 0);
    localParts.push(local, name, data);

    const central = Buffer.alloc(46);
    writeUInt32(central, 0, 0x02014b50);
    writeUInt16(central, 4, 20);
    writeUInt16(central, 6, 20);
    writeUInt16(central, 8, flags);
    writeUInt16(central, 10, method);
    writeUInt16(central, 12, dosTime);
    writeUInt16(central, 14, dosDate);
    writeUInt32(central, 16, crc);
    writeUInt32(central, 20, data.length);
    writeUInt32(central, 24, raw.length);
    writeUInt16(central, 28, name.length);
    writeUInt16(central, 30, 0);
    writeUInt16(central, 32, 0);
    writeUInt16(central, 34, 0);
    writeUInt16(central, 36, 0);
    writeUInt32(central, 38, 0);
    writeUInt32(central, 42, offset);
    centralParts.push(central, name);

    offset += local.length + name.length + data.length;
  }

  const centralDirectory = Buffer.concat(centralParts);
  const end = Buffer.alloc(22);
  writeUInt32(end, 0, 0x06054b50);
  writeUInt16(end, 4, 0);
  writeUInt16(end, 6, 0);
  writeUInt16(end, 8, entries.length);
  writeUInt16(end, 10, entries.length);
  writeUInt32(end, 12, centralDirectory.length);
  writeUInt32(end, 16, offset);
  writeUInt16(end, 20, 0);

  return Buffer.concat([...localParts, centralDirectory, end]);
}

function cleanXml(value) {
  return escapeHtml(String(value ?? '').replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, ''));
}

function xmlHead(title, prefix) {
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="ru" lang="ru">
<head>
<meta charset="UTF-8"/>
<title>${cleanXml(title)}</title>
<link rel="stylesheet" type="text/css" href="${prefix}style.css"/>
</head>`;
}

function chapterXhtml({ title, html }, index) {
  return `${xmlHead(title, '../')}
<body>
<section epub:type="chapter" id="chapter-${index + 1}">
<h1>${cleanXml(title)}</h1>
${html}
</section>
</body>
</html>
`;
}

function coverXhtml({ title, coverHref }) {
  return `${xmlHead('Обложка', '../')}
<body>
<section epub:type="cover" id="cover">
<img class="cover-image" src="${cleanXml(coverHref)}" alt="Обложка: ${cleanXml(title)}"/>
</section>
</body>
</html>
`;
}

function titleXhtml(metadata) {
  const authors = metadata.authors?.length ? `<p class="authors">${cleanXml(metadata.authors.join(', '))}</p>` : '';
  const description = metadata.description ? `<div class="description">${metadata.description.split(/\n{2,}/).map((p) => `<p>${cleanXml(p).replace(/\n/g, '<br/>')}</p>`).join('')}</div>` : '';
  const source = metadata.sourceUrl
    ? `<p class="source">Источник: <a href="${cleanXml(metadata.sourceUrl)}">${cleanXml(metadata.sourceUrl)}</a></p>`
    : '';
  return `${xmlHead(metadata.title, '../')}
<body>
<section epub:type="titlepage" id="titlepage">
<h1>${cleanXml(metadata.title)}</h1>
${authors}
${description}
${source}
</section>
</body>
</html>
`;
}

function navXhtml({ title, entries }) {
  const items = entries
    .map((entry) => `<li><a href="${cleanXml(entry.href)}">${cleanXml(entry.label)}</a></li>`)
    .join('\n');
  return `${xmlHead(title, '')}
<body>
<nav epub:type="toc" id="toc">
<h1>Содержание</h1>
<ol>
${items}
</ol>
</nav>
</body>
</html>
`;
}

function ncx({ identifier, title, entries }) {
  const points = entries
    .map(
      (entry, index) => `<navPoint id="navPoint-${index + 1}" playOrder="${index + 1}">
<navLabel><text>${cleanXml(entry.label)}</text></navLabel>
<content src="${cleanXml(entry.href)}"/>
</navPoint>`,
    )
    .join('\n');
  return `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head>
<meta name="dtb:uid" content="${cleanXml(identifier)}"/>
<meta name="dtb:depth" content="1"/>
<meta name="dtb:totalPageCount" content="0"/>
<meta name="dtb:maxPageNumber" content="0"/>
</head>
<docTitle><text>${cleanXml(title)}</text></docTitle>
<navMap>
${points}
</navMap>
</ncx>
`;
}

function safeFileName(value, fallback) {
  const name = String(value ?? '')
    .replace(/[\\/:*?"<>|\u0000-\u001f]/g, '-')
    .replace(/\s+/g, ' ')
    .trim();
  return name || fallback;
}

function imageEntry(image, index) {
  const mediaType = detectImageMediaType(image.bytes, normalizeMediaType(image.mediaType));
  if (!mediaType || !isSupportedImageMediaType(mediaType)) {
    throw new TypeError(`Unsupported EPUB image media type: ${image.mediaType ?? 'unknown'}`);
  }
  const extension = imageExtension(mediaType);
  const fileName = safeFileName(image.fileName, `image-${String(index + 1).padStart(3, '0')}.${extension}`);
  const base = fileName.includes('.') ? fileName : `${fileName}.${extension}`;
  return { href: `images/${base}`, mediaType, bytes: image.bytes };
}

export const EPUB_STYLESHEET = `
body { font-family: serif; line-height: 1.55; margin: 1.5em; }
h1 { font-size: 1.5em; line-height: 1.25; margin: 0 0 1em; }
h2, h3, h4, h5, h6 { line-height: 1.3; margin: 1.4em 0 0.6em; }
p { margin: 0 0 0.9em; text-align: justify; }
blockquote { margin: 1em 1.5em; padding-left: 1em; border-left: 3px solid #bbb; }
pre { white-space: pre-wrap; overflow-wrap: break-word; }
img { max-width: 100%; height: auto; }
.cover-image { display: block; max-width: 100%; max-height: 100vh; margin: 0 auto; }
.image-placeholder { color: #666; font-style: italic; }
.underline { text-decoration: underline; }
.authors { font-size: 1.1em; }
.description { margin-top: 1.5em; }
.source { margin-top: 2em; font-size: 0.85em; overflow-wrap: break-word; }
`.trim();

/**
 * Build a complete EPUB 3 file.
 *
 * @param {object} input
 * @param {object} input.metadata title, authors, description, genres, tags, sourceUrl, slug, releaseDate
 * @param {Array<{title: string, html: string}>} input.chapters rendered XHTML fragments
 * @param {{bytes: Uint8Array, mediaType: string, fileName?: string}|null} [input.cover]
 * @param {Array<{bytes: Uint8Array, mediaType: string, fileName?: string}>} [input.images]
 * @param {string} [input.identifier]
 * @param {string} [input.language]
 * @param {Date} [input.modifiedAt]
 * @returns {Buffer}
 */
export function buildEpub({
  metadata,
  chapters,
  cover = null,
  images = [],
  identifier = null,
  language = 'ru',
  modifiedAt = new Date(),
}) {
  if (!metadata || typeof metadata !== 'object') throw new TypeError('metadata is required');
  if (typeof metadata.title !== 'string' || metadata.title.trim() === '') throw new TypeError('metadata.title is required');
  if (!Array.isArray(chapters) || chapters.length === 0) throw new TypeError('at least one chapter is required');

  const bookId = identifier ?? `urn:ranobelib:${encodeURIComponent(metadata.slug ?? metadata.id ?? 'book')}`;
  const chapterDocs = chapters.map((chapter, index) => {
    const fileName = `chapter-${String(index + 1).padStart(3, '0')}.xhtml`;
    const hasRemoteImages = /<img\b[^>]*\bsrc="https?:/i.test(chapter.html ?? '');
    return {
      id: `chapter-${index + 1}`,
      href: `text/${fileName}`,
      path: `OEBPS/text/${fileName}`,
      title: String(chapter.title ?? `Глава ${index + 1}`),
      html: String(chapter.html ?? ''),
      properties: hasRemoteImages ? 'remote-resources' : null,
    };
  });

  const imageDocs = images.map((image, index) => {
    if (!image || !image.bytes) throw new TypeError('every image needs bytes');
    return { id: `image-${index + 1}`, ...imageEntry(image, index) };
  });

  let coverDoc = null;
  if (cover) {
    if (!cover.bytes) throw new TypeError('cover needs bytes');
    const mediaType = detectImageMediaType(cover.bytes, normalizeMediaType(cover.mediaType));
    if (!mediaType || !isSupportedImageMediaType(mediaType)) throw new TypeError('cover image type is not supported by EPUB');
    coverDoc = { href: `images/cover.${imageExtension(mediaType)}`, mediaType, bytes: cover.bytes };
  }

  const titlePage = metadata.description || metadata.authors?.length
    ? { id: 'title-page', href: 'text/title.xhtml', path: 'OEBPS/text/title.xhtml', title: 'Титульная страница' }
    : null;
  const coverPage = coverDoc ? { id: 'cover-page', href: 'text/cover.xhtml', path: 'OEBPS/text/cover.xhtml', title: 'Обложка' } : null;

  const manifest = [
    '<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>',
    '<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>',
    '<item id="css" href="style.css" media-type="text/css"/>',
    ...(coverDoc ? [`<item id="cover-image" href="${cleanXml(coverDoc.href)}" media-type="${coverDoc.mediaType}" properties="cover-image"/>`] : []),
    ...(coverPage ? [`<item id="${coverPage.id}" href="${coverPage.href}" media-type="application/xhtml+xml"/>`] : []),
    ...(titlePage ? [`<item id="${titlePage.id}" href="${titlePage.href}" media-type="application/xhtml+xml"/>`] : []),
    ...chapterDocs.map(
      (chapter) =>
        `<item id="${chapter.id}" href="${chapter.href}" media-type="application/xhtml+xml"${chapter.properties ? ` properties="${chapter.properties}"` : ''}/>`,
    ),
    ...imageDocs.map((image) => `<item id="${image.id}" href="${image.href}" media-type="${image.mediaType}"/>`),
  ].join('\n    ');

  const spine = [
    ...(coverPage ? [`<itemref idref="${coverPage.id}"/>`] : []),
    ...(titlePage ? [`<itemref idref="${titlePage.id}"/>`] : []),
    ...chapterDocs.map((chapter) => `<itemref idref="${chapter.id}"/>`),
  ].join('\n    ');

  const subjects = [...(metadata.genres ?? []), ...(metadata.tags ?? [])];
  const uniqueSubjects = [...new Map(subjects.map((subject) => [String(subject).toLocaleLowerCase('ru'), String(subject)])).values()];
  const creators = (metadata.authors ?? []).map((author) => `    <dc:creator>${cleanXml(author)}</dc:creator>`).join('\n');
  const description = metadata.description ? `    <dc:description>${cleanXml(metadata.description)}</dc:description>\n` : '';
  const date = /^\d{4}/.test(String(metadata.releaseDate ?? '')) ? `    <dc:date>${cleanXml(metadata.releaseDate)}</dc:date>\n` : '';
  const source = metadata.sourceUrl ? `    <dc:source>${cleanXml(metadata.sourceUrl)}</dc:source>\n` : '';
  const subjectXml = uniqueSubjects.map((subject) => `    <dc:subject>${cleanXml(subject)}</dc:subject>`).join('\n');
  const modified = (modifiedAt instanceof Date && !Number.isNaN(modifiedAt.valueOf()) ? modifiedAt : new Date()).toISOString().replace(/\.\d{3}Z$/, 'Z');

  const opf = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id" xml:lang="${cleanXml(language)}">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">${cleanXml(bookId)}</dc:identifier>
    <dc:title>${cleanXml(metadata.title)}</dc:title>
    <dc:language>${cleanXml(language)}</dc:language>
${creators}
${description}${date}${source}${subjectXml}
    <meta property="dcterms:modified">${modified}</meta>${coverDoc ? '\n    <meta name="cover" content="cover-image"/>' : ''}
  </metadata>
  <manifest>
    ${manifest}
  </manifest>
  <spine toc="ncx">
    ${spine}
  </spine>
</package>
`;

  const navEntries = chapterDocs.map((chapter) => ({ href: chapter.href, label: chapter.title }));
  const entries = [
    { path: 'mimetype', data: Buffer.from('application/epub+zip', 'ascii'), compress: false },
    { path: 'META-INF/container.xml', data: Buffer.from(containerXml(), 'utf8') },
    { path: 'OEBPS/content.opf', data: Buffer.from(opf, 'utf8') },
    { path: 'OEBPS/style.css', data: Buffer.from(EPUB_STYLESHEET, 'utf8') },
    { path: 'OEBPS/nav.xhtml', data: Buffer.from(navXhtml({ title: metadata.title, entries: navEntries }), 'utf8') },
    { path: 'OEBPS/toc.ncx', data: Buffer.from(ncx({ identifier: bookId, title: metadata.title, entries: navEntries }), 'utf8') },
    ...(coverPage ? [{ path: coverPage.path, data: Buffer.from(coverXhtml({ title: metadata.title, coverHref: `../${coverDoc.href}` }), 'utf8') }] : []),
    ...(titlePage ? [{ path: titlePage.path, data: Buffer.from(titleXhtml(metadata), 'utf8') }] : []),
    ...chapterDocs.map((chapter, index) => ({ path: chapter.path, data: Buffer.from(chapterXhtml(chapter, index), 'utf8') })),
    ...imageDocs.map((image) => ({ path: `OEBPS/${image.href}`, data: Buffer.from(image.bytes) })),
    ...(coverDoc ? [{ path: `OEBPS/${coverDoc.href}`, data: Buffer.from(coverDoc.bytes) }] : []),
  ];

  return createZip(entries, { modifiedAt });
}

function containerXml() {
  return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`;
}

/** A file name that is safe on every platform and still recognisable. */
export function bookFileName(title, slug = null) {
  const base = safeFileName(slug ? `${title} (${slug})` : title, 'ranobelib-book')
    .replace(/[. ]+$/, '')
    .slice(0, 150);
  return `${base}.epub`;
}
