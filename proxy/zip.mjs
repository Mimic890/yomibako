/**
 * Minimal streaming ZIP writer, stored (no compression) only.
 *
 * CBZ is a ZIP of images that are already compressed, so storing them costs nothing and keeps the
 * writer small. Entries are written straight to disk: a full manga is hundreds of megabytes and
 * would not fit comfortably in memory.
 *
 * shortcut: no ZIP64, no compression, no timestamps; a single archive over 4 GiB or 65 535 files
 * fails loudly. Upgrade to a real zip library if archives that large become normal.
 */
import { open, rm } from 'node:fs/promises';

const LOCAL_SIG = 0x04034b50;
const CENTRAL_SIG = 0x02014b50;
const EOCD_SIG = 0x06054b50;
const UTF8_FLAG = 0x0800;
const MAX_UINT32 = 0xffffffff;
const MAX_ENTRIES = 0xffff;
/** A fixed, valid DOS timestamp: readers dislike the 1980-00-00 that zero produces. */
const DOS_DATE = ((2026 - 1980) << 9) | (1 << 5) | 1;

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  return table;
})();

function crc32(buffer) {
  let crc = 0xffffffff;
  for (let i = 0; i < buffer.length; i++) crc = CRC_TABLE[(crc ^ buffer[i]) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

export class ZipWriter {
  #handle;
  #offset = 0;
  #entries = [];

  constructor(path) {
    this.path = path;
  }

  async open() {
    this.#handle = await open(this.path, 'w');
    return this;
  }

  async add(name, data) {
    if (this.#entries.length >= MAX_ENTRIES) throw new Error('ZIP has too many entries');
    const nameBytes = Buffer.from(name, 'utf8');
    const body = Buffer.isBuffer(data) ? data : Buffer.from(data);
    if (body.length > MAX_UINT32) throw new Error('ZIP entry is too large');
    const crc = crc32(body);

    const header = Buffer.alloc(30 + nameBytes.length);
    header.writeUInt32LE(LOCAL_SIG, 0);
    header.writeUInt16LE(20, 4);
    header.writeUInt16LE(UTF8_FLAG, 6);
    header.writeUInt16LE(0, 8);
    header.writeUInt16LE(DOS_DATE, 12);
    header.writeUInt32LE(crc, 14);
    header.writeUInt32LE(body.length, 18);
    header.writeUInt32LE(body.length, 22);
    header.writeUInt16LE(nameBytes.length, 26);
    nameBytes.copy(header, 30);

    const offset = this.#offset;
    await this.#write(header);
    await this.#write(body);
    this.#entries.push({ nameBytes, crc, size: body.length, offset });
  }

  async close() {
    const centralStart = this.#offset;
    for (const entry of this.#entries) {
      const header = Buffer.alloc(46 + entry.nameBytes.length);
      header.writeUInt32LE(CENTRAL_SIG, 0);
      header.writeUInt16LE(20, 4);
      header.writeUInt16LE(20, 6);
      header.writeUInt16LE(UTF8_FLAG, 8);
      header.writeUInt16LE(0, 10);
      header.writeUInt16LE(DOS_DATE, 14);
      header.writeUInt32LE(entry.crc, 16);
      header.writeUInt32LE(entry.size, 20);
      header.writeUInt32LE(entry.size, 24);
      header.writeUInt16LE(entry.nameBytes.length, 28);
      header.writeUInt32LE(entry.offset, 42);
      entry.nameBytes.copy(header, 46);
      await this.#write(header);
    }

    const eocd = Buffer.alloc(22);
    eocd.writeUInt32LE(EOCD_SIG, 0);
    eocd.writeUInt16LE(this.#entries.length, 8);
    eocd.writeUInt16LE(this.#entries.length, 10);
    eocd.writeUInt32LE(this.#offset - centralStart, 12);
    eocd.writeUInt32LE(centralStart, 16);
    await this.#write(eocd);

    await this.#handle.close();
    this.#handle = null;
    return this.path;
  }

  async abort() {
    if (this.#handle) await this.#handle.close().catch(() => {});
    this.#handle = null;
    await rm(this.path, { force: true }).catch(() => {});
  }

  async #write(buffer) {
    await this.#handle.write(buffer);
    this.#offset += buffer.length;
  }
}
