import { inflateRawSync } from 'node:zlib';

/**
 * Minimal reader for the ZIPs the EPUB writer produces. It reads local file headers in order, which
 * is enough for assertions about mimetype placement, compression and entry contents.
 */
export function readZip(bytes) {
  const buffer = Buffer.isBuffer(bytes) ? bytes : Buffer.from(bytes);
  const entries = [];
  let offset = 0;

  while (offset + 4 <= buffer.length) {
    const signature = buffer.readUInt32LE(offset);
    if (signature !== 0x04034b50) break;

    const flags = buffer.readUInt16LE(offset + 6);
    const method = buffer.readUInt16LE(offset + 8);
    const crc = buffer.readUInt32LE(offset + 14);
    const compressedSize = buffer.readUInt32LE(offset + 18);
    const uncompressedSize = buffer.readUInt32LE(offset + 22);
    const nameLength = buffer.readUInt16LE(offset + 26);
    const extraLength = buffer.readUInt16LE(offset + 28);
    const name = buffer.subarray(offset + 30, offset + 30 + nameLength).toString('utf8');
    const dataStart = offset + 30 + nameLength + extraLength;
    const data = buffer.subarray(dataStart, dataStart + compressedSize);

    if (flags & 0x08) throw new Error(`Unexpected data descriptor in ${name}`);
    let content;
    if (method === 0) content = Buffer.from(data);
    else if (method === 8) content = inflateRawSync(data);
    else throw new Error(`Unsupported ZIP method ${method} in ${name}`);

    if (content.length !== uncompressedSize) throw new Error(`Uncompressed size mismatch in ${name}`);
    entries.push({ name, method, flags, crc, compressedSize, uncompressedSize, content });
    offset = dataStart + compressedSize;
  }

  const map = new Map(entries.map((entry) => [entry.name, entry]));
  return {
    entries,
    map,
    names: entries.map((entry) => entry.name),
    text(name) {
      const entry = map.get(name);
      if (!entry) throw new Error(`Missing ZIP entry: ${name}`);
      return entry.content.toString('utf8');
    },
    has(name) {
      return map.has(name);
    },
  };
}
