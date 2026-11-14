import assert from 'node:assert/strict';
import test from 'node:test';

import { collectImageAttachments, documentToHtml, documentToText, htmlToText, plainTextToHtml } from '../src/document.mjs';

const attachments = [
  { id: 'img-1', name: 'img-1', filename: 'img-1.png', url: 'https://cdn.example.invalid/chapters/img-1.png' },
];

test('text is escaped and document structure becomes safe XHTML', () => {
  const document = {
    type: 'doc',
    content: [
      { type: 'heading', attrs: { level: 1 }, content: [{ type: 'text', text: 'Заголовок' }] },
      { type: 'paragraph', content: [{ type: 'text', text: '<script>alert("x")</script> & текст' }] },
      { type: 'paragraph', content: [{ type: 'bold', content: [{ type: 'text', text: 'жирный' }] }] },
      { type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'пункт' }] }] }] },
      { type: 'horizontalRule' },
    ],
  };

  const html = documentToHtml(document);
  assert.match(html, /<h2>Заголовок<\/h2>/);
  assert.match(html, /&lt;script&gt;alert\(&quot;x&quot;\)&lt;\/script&gt; &amp; текст/);
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /<strong>жирный<\/strong>/);
  assert.match(html, /<ul><li><p>пункт<\/p><\/li><\/ul>/);
  assert.match(html, /<hr\/>/);
});

test('links are limited to safe schemes and unknown nodes keep their text', () => {
  const html = documentToHtml({
    type: 'doc',
    content: [
      { type: 'paragraph', content: [{ type: 'link', attrs: { href: 'javascript:alert(1)' }, content: [{ type: 'text', text: 'плохая' }] }] },
      { type: 'paragraph', content: [{ type: 'link', attrs: { href: 'https://example.invalid/a?b=1&c=2' }, content: [{ type: 'text', text: 'хорошая' }] }] },
      { type: 'paragraph', content: [{ type: 'unknownNode', content: [{ type: 'text', text: 'сохранённый текст' }] }] },
    ],
  });

  assert.doesNotMatch(html, /javascript:/);
  assert.match(html, /<a href="https:\/\/example\.invalid\/a\?b=1&amp;c=2">хорошая<\/a>/);
  assert.match(html, /сохранённый текст/);
});

test('images resolve by attachment id and can be rewritten locally', () => {
  const document = {
    type: 'doc',
    content: [
      { type: 'image', attrs: { images: [{ image: 'img-1' }], alt: 'Иллюстрация' } },
      { type: 'image', attrs: { src: 'https://cdn.example.invalid/remote.png', alt: 'Удалённая' } },
    ],
  };

  const images = collectImageAttachments(document, attachments);
  assert.deepEqual(
    images.map((image) => image.url),
    ['https://cdn.example.invalid/chapters/img-1.png', 'https://cdn.example.invalid/remote.png'],
  );

  const remote = documentToHtml(document, attachments);
  assert.match(remote, /src="https:\/\/cdn\.example\.invalid\/chapters\/img-1\.png"/);

  const local = documentToHtml(document, attachments, {
    imageResolver: ({ url }) => (url.includes('img-1') ? '../images/local.png' : null),
  });
  assert.match(local, /src="\.\.\/images\/local\.png"/);
  assert.match(local, /<p class="image-placeholder">\[Удалённая\]<\/p>/);
});

test('plain text and HTML fallbacks keep paragraphs and decode common entities', () => {
  assert.equal(documentToText({ type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Первый' }] }, { type: 'paragraph', content: [{ type: 'text', text: 'Второй' }] }] }), 'Первый\n\nВторой');
  assert.equal(htmlToText('<p>Первый &amp; второй</p><p>Третий&nbsp;абзац</p>'), 'Первый & второй\n\nТретий абзац');
  assert.equal(plainTextToHtml('Строка <1>\n\nВторой & абзац'), '<p>Строка &lt;1&gt;</p>\n<p>Второй &amp; абзац</p>');
});
