/**
 * Rendering for RanobeLib documents.
 *
 * The API returns two shapes:
 *  - a ProseMirror-like document (`{ type: 'doc', content: [...] }`) for summaries and chapters;
 *  - occasionally a plain string for older records.
 *
 * Everything here is deliberately free of HTML-parsing dependencies. Text is escaped before it is
 * put into XHTML, and unknown node types are rendered through their children rather than dumped as
 * JSON. That keeps a malformed document from becoming a broken EPUB.
 */

const HTML_ENTITIES = new Map([
  ['nbsp', ' '],
  ['amp', '&'],
  ['lt', '<'],
  ['gt', '>'],
  ['quot', '"'],
  ['apos', "'"],
  ['#39', "'"],
]);

export function escapeHtml(value) {
  return String(value ?? '')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function validCodePoint(code) {
  return Number.isInteger(code) && code >= 0 && code <= 0x10ffff;
}

function decodeEntities(value) {
  return value.replace(/&(#x?[0-9a-f]+|[a-z]+);/gi, (match, entity) => {
    const key = entity.toLowerCase();
    if (key.startsWith('#x')) {
      const code = Number.parseInt(key.slice(2), 16);
      return validCodePoint(code) ? String.fromCodePoint(code) : match;
    }
    if (key.startsWith('#')) {
      const code = Number.parseInt(key.slice(1), 10);
      return validCodePoint(code) ? String.fromCodePoint(code) : match;
    }
    return HTML_ENTITIES.get(key) ?? match;
  });
}

/** Best-effort plain text from an HTML string, used only for summaries returned as HTML. */
export function htmlToText(html) {
  return decodeEntities(
    String(html ?? '')
      .replace(/<br\s*\/?>/gi, '\n')
      .replace(/<\/(p|div|li|h[1-6]|blockquote|tr)>/gi, '\n\n')
      .replace(/<[^>]*>/g, ''),
  )
    .replace(/[ \t]*\n[ \t]*/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

/** Plain text from a string or a document. Paragraphs stay separated by a blank line. */
export function documentToText(document) {
  if (document == null) return '';
  if (typeof document === 'string') return htmlToText(document);
  if (typeof document !== 'object') return String(document);
  if (document.type === 'doc') return childrenToText(document.content).trim();
  return nodeToText(document).trim();
}

function childrenToText(children) {
  if (!Array.isArray(children)) return '';
  return children.map(nodeToText).join('');
}

function nodeToText(node) {
  if (!node || typeof node !== 'object') return '';
  const content = Array.isArray(node.content) ? node.content : [];
  switch (node.type) {
    case 'text':
      return String(node.text ?? '');
    case 'hardBreak':
      return '\n';
    case 'horizontalRule':
      return '\n';
    case 'paragraph':
    case 'heading':
    case 'blockquote':
    case 'codeBlock':
      return `${childrenToText(content).trim()}\n\n`;
    case 'listItem':
      return `- ${childrenToText(content).trim()}\n`;
    case 'orderedList':
    case 'bulletList':
      return `${childrenToText(content)}\n`;
    case 'image':
      return node.attrs?.alt ? `[${node.attrs.alt}]` : '';
    default:
      return childrenToText(content);
  }
}

function safeHref(raw) {
  const value = String(raw ?? '').trim();
  if (/^(https?:|mailto:)/i.test(value)) return value;
  return null;
}

function findAttachment(attachments, reference) {
  if (!Array.isArray(attachments) || reference == null) return null;
  return (
    attachments.find(
      (attachment) =>
        String(attachment.id) === String(reference) ||
        String(attachment.name) === String(reference) ||
        String(attachment.filename) === String(reference),
    ) ?? null
  );
}

function imageReferences(node) {
  const images = node?.attrs?.images;
  if (Array.isArray(images) && images.length > 0) {
    return images.map((entry) => (entry && typeof entry === 'object' ? entry.image : entry)).filter((value) => value != null);
  }
  if (node?.attrs?.src) return [node.attrs.src];
  return [];
}

/**
 * Which image attachments a document actually uses.
 *
 * Returned as a flat list with the resolved URL, so a caller can download each image once and then
 * render the document again with local hrefs.
 */
export function collectImageAttachments(document, attachments, { alt = null } = {}) {
  const found = new Map();
  walkDocument(document, (node) => {
    if (node.type !== 'image') return;
    for (const reference of imageReferences(node)) {
      const attachment = findAttachment(attachments, reference);
      const url = attachment?.url ?? (/^https?:/i.test(String(reference)) ? String(reference) : null);
      if (!url) continue;
      if (!found.has(url)) {
        found.set(url, {
          url,
          id: attachment?.id ?? null,
          name: attachment?.name ?? null,
          fileName: attachment?.filename ?? null,
          alt: node.attrs?.alt ?? alt,
        });
      }
    }
  });
  return [...found.values()];
}

function walkDocument(node, visit) {
  if (!node || typeof node !== 'object') return;
  visit(node);
  if (Array.isArray(node.content)) {
    for (const child of node.content) walkDocument(child, visit);
  }
}

/**
 * Render a RanobeLib document as an XHTML fragment.
 *
 * `imageResolver(ref)` may return a local href for an attachment/URL. When it returns null the
 * image is rendered as an accessible placeholder, which keeps the EPUB self-contained.
 */
export function documentToHtml(document, attachments = [], { imageResolver = null } = {}) {
  if (document == null) return '';
  if (typeof document === 'string') return plainTextToHtml(htmlToText(document));
  if (typeof document !== 'object') return plainTextToHtml(String(document));
  if (document.type === 'doc') return renderChildren(document.content, attachments, imageResolver).trim();
  return renderNode(document, attachments, imageResolver).trim();
}

export function plainTextToHtml(value) {
  return String(value ?? '')
    .replace(/\r\n/g, '\n')
    .split(/\n{2,}/)
    .map((paragraph) => `<p>${escapeHtml(paragraph).replace(/\n/g, '<br/>')}</p>`)
    .join('\n');
}

function renderChildren(children, attachments, imageResolver) {
  if (!Array.isArray(children)) return '';
  return children.map((child) => renderNode(child, attachments, imageResolver)).join('');
}

function renderNode(node, attachments, imageResolver) {
  if (!node || typeof node !== 'object') return '';
  const content = Array.isArray(node.content) ? node.content : [];
  const inner = () => renderChildren(content, attachments, imageResolver);

  switch (node.type) {
    case 'doc':
      return inner();
    case 'text':
      return escapeHtml(node.text ?? '').replace(/\n/g, '<br/>');
    case 'hardBreak':
      return '<br/>';
    case 'horizontalRule':
      return '<hr/>';
    case 'paragraph':
      return `<p>${inner() || '<br/>'}</p>`;
    case 'heading':
      return `<h${headingLevel(node)}>${inner()}</h${headingLevel(node)}>`;
    case 'blockquote':
      return `<blockquote>${inner()}</blockquote>`;
    case 'orderedList':
      return `<ol>${inner()}</ol>`;
    case 'bulletList':
      return `<ul>${inner()}</ul>`;
    case 'listItem':
      return `<li>${inner()}</li>`;
    case 'bold':
      return `<strong>${inner()}</strong>`;
    case 'italic':
      return `<em>${inner()}</em>`;
    case 'underline':
      return `<span class="underline">${inner()}</span>`;
    case 'strike':
      return `<s>${inner()}</s>`;
    case 'code':
      return `<code>${inner()}</code>`;
    case 'codeBlock':
      return `<pre><code>${inner()}</code></pre>`;
    case 'link': {
      const href = safeHref(node.attrs?.href);
      return href ? `<a href="${escapeHtml(href)}">${inner()}</a>` : inner();
    }
    case 'image':
      return renderImage(node, attachments, imageResolver);
    default:
      // Unknown node: keep its text rather than a JSON dump or a validation failure.
      return inner();
  }
}

function headingLevel(node) {
  const raw = Number(node.attrs?.level);
  if (!Number.isFinite(raw)) return 2;
  return Math.min(6, Math.max(2, Math.round(raw) + 1));
}

function renderImage(node, attachments, imageResolver) {
  const alt = node.attrs?.alt ? String(node.attrs.alt) : '';
  for (const reference of imageReferences(node)) {
    const attachment = findAttachment(attachments, reference);
    const url = attachment?.url ?? (/^https?:/i.test(String(reference)) ? String(reference) : null);
    if (!url) continue;
    const resolved = imageResolver ? imageResolver({ url, attachment, alt }) : url;
    if (resolved) {
      return `<img src="${escapeHtml(resolved)}" alt="${escapeHtml(alt)}"/>`;
    }
  }
  return alt ? `<p class="image-placeholder">[${escapeHtml(alt)}]</p>` : '';
}
