// A small zip reader and writer for the tests: enough for the settings zips
// omo writes (stored or deflated entries, no zip64) and for writing one back
// with an entry changed.
import { crc32, deflateRawSync, inflateRawSync } from 'node:zlib'

/** The entries of a zip by name. */
export function readZip(data: Buffer): Map<string, Buffer> {
  let eocd = data.length - 22
  while (eocd >= 0 && data.readUInt32LE(eocd) !== 0x06054b50) eocd--
  if (eocd < 0) throw new Error('not a zip file')
  const count = data.readUInt16LE(eocd + 10)
  let p = data.readUInt32LE(eocd + 16)
  const out = new Map<string, Buffer>()
  for (let i = 0; i < count; i++) {
    if (data.readUInt32LE(p) !== 0x02014b50) throw new Error('broken central directory')
    const method = data.readUInt16LE(p + 10)
    const size = data.readUInt32LE(p + 20)
    const nameLen = data.readUInt16LE(p + 28)
    const extraLen = data.readUInt16LE(p + 30)
    const commentLen = data.readUInt16LE(p + 32)
    const local = data.readUInt32LE(p + 42)
    const name = data.toString('utf8', p + 46, p + 46 + nameLen)
    const start = local + 30 + data.readUInt16LE(local + 26) + data.readUInt16LE(local + 28)
    const raw = data.subarray(start, start + size)
    out.set(name, method === 8 ? inflateRawSync(raw) : Buffer.from(raw))
    p += 46 + nameLen + extraLen + commentLen
  }
  return out
}

/** A zip of the entries, deflated. */
export function writeZip(entries: Map<string, Buffer>): Buffer {
  const locals: Buffer[] = []
  const centrals: Buffer[] = []
  let offset = 0
  for (const [name, content] of entries) {
    const nameBuf = Buffer.from(name, 'utf8')
    const packed = deflateRawSync(content)
    const crc = crc32(content)
    const local = Buffer.alloc(30)
    local.writeUInt32LE(0x04034b50, 0)
    local.writeUInt16LE(20, 4)
    local.writeUInt16LE(0x0800, 6)
    local.writeUInt16LE(8, 8)
    local.writeUInt32LE(crc, 14)
    local.writeUInt32LE(packed.length, 18)
    local.writeUInt32LE(content.length, 22)
    local.writeUInt16LE(nameBuf.length, 26)
    const central = Buffer.alloc(46)
    central.writeUInt32LE(0x02014b50, 0)
    central.writeUInt16LE(0x0314, 4)
    central.writeUInt16LE(20, 6)
    central.writeUInt16LE(0x0800, 8)
    central.writeUInt16LE(8, 10)
    central.writeUInt32LE(crc, 16)
    central.writeUInt32LE(packed.length, 20)
    central.writeUInt32LE(content.length, 24)
    central.writeUInt16LE(nameBuf.length, 28)
    // A regular file, mode 0600, in the high half of the external attributes.
    central.writeUInt32LE((0o100600 << 16) >>> 0, 38)
    central.writeUInt32LE(offset, 42)
    locals.push(local, nameBuf, packed)
    centrals.push(central, nameBuf)
    offset += local.length + nameBuf.length + packed.length
  }
  const dir = Buffer.concat(centrals)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0)
  end.writeUInt16LE(entries.size, 8)
  end.writeUInt16LE(entries.size, 10)
  end.writeUInt32LE(dir.length, 12)
  end.writeUInt32LE(offset, 16)
  return Buffer.concat([...locals, dir, end])
}
