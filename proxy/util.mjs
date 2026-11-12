/** A title reaches a filesystem path, so it is reduced to something a filename can hold. */
export function sanitizeName(title) {
  const cleaned = String(title ?? '')
    .replace(/[\u0000-\u001f<>:"/\\|?*]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 150)
    .replace(/[. ]+$/, '');
  return cleaned || 'book';
}
