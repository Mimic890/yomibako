/** Small image helpers shared by the HTTP client and the EPUB writer. */

export function normalizeMediaType(value) {
  if (typeof value !== 'string') return null;
  const mediaType = value.split(';', 1)[0].trim().toLowerCase();
  return mediaType || null;
}

export function detectImageMediaType(bytes, fallback = null) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes ?? []);
  if (data.length >= 8 && data[0] === 0x89 && data[1] === 0x50 && data[2] === 0x4e && data[3] === 0x47) return 'image/png';
  if (data.length >= 3 && data[0] === 0xff && data[1] === 0xd8 && data[2] === 0xff) return 'image/jpeg';
  if (data.length >= 6 && data[0] === 0x47 && data[1] === 0x49 && data[2] === 0x46) return 'image/gif';
  if (
    data.length >= 12 &&
    data[0] === 0x52 &&
    data[1] === 0x49 &&
    data[2] === 0x46 &&
    data[3] === 0x46 &&
    data[8] === 0x57 &&
    data[9] === 0x45 &&
    data[10] === 0x42 &&
    data[11] === 0x50
  ) {
    return 'image/webp';
  }
  const head = new TextDecoder().decode(data.subarray(0, Math.min(data.length, 512))).trimStart().toLowerCase();
  if (head.startsWith('<svg') || (head.startsWith('<?xml') && head.includes('<svg'))) return 'image/svg+xml';
  return normalizeMediaType(fallback);
}

const EXTENSIONS = new Map([
  ['image/jpeg', 'jpg'],
  ['image/jpg', 'jpg'],
  ['image/png', 'png'],
  ['image/gif', 'gif'],
  ['image/webp', 'webp'],
  ['image/svg+xml', 'svg'],
]);

export function imageExtension(mediaType) {
  return EXTENSIONS.get(normalizeMediaType(mediaType)) ?? null;
}

/** EPUB 3 readers are not required to understand formats outside the core image set. */
export function isSupportedImageMediaType(mediaType) {
  return imageExtension(mediaType) !== null;
}
