import { crc32, deflateSync } from 'node:zlib'
import type { Locator, Page } from '@playwright/test'
import type { Omo } from './omo'

/** A real w×h PNG (stripes), built here so the test needs no file on disk. */
export function png(w = 48, h = 32): Buffer {
  const chunk = (type: string, data: Buffer) => {
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(crc32(body))
    return Buffer.concat([len, body, crc])
  }
  const head = Buffer.alloc(13)
  head.writeUInt32BE(w, 0)
  head.writeUInt32BE(h, 4)
  head.writeUInt8(8, 8) // bit depth
  head.writeUInt8(2, 9) // RGB
  const rows: Buffer[] = []
  for (let y = 0; y < h; y++) {
    const row = Buffer.alloc(1 + w * 3)
    for (let x = 0; x < w; x++) row.set(x % 8 < 4 ? [187, 154, 221] : [140, 201, 173], 1 + x * 3)
    rows.push(row)
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', head),
    chunk('IDAT', deflateSync(Buffer.concat(rows))),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

/** Chooses files through "Add image…" and its file picker. */
export async function pick(page: Page, scope: Locator, files: { name: string; mimeType: string; buffer: Buffer }[]) {
  const chooser = page.waitForEvent('filechooser')
  await scope.locator('[data-add-image]').click()
  await (await chooser).setFiles(files)
}


/** Uploads an image to a ticket the way the board does and returns the Markdown that shows it. */
export async function uploadImage(omo: Omo, n: number, name: string, bytes: Buffer = png()): Promise<string> {
  const res = await fetch(`${omo.base}/api/tickets/${n}/images`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${omo.token}`, 'Content-Type': 'application/octet-stream', 'Omo-Image-Name': encodeURIComponent(name) },
    body: new Uint8Array(bytes),
  })
  if (res.status !== 201) throw new Error(`upload of ${name}: ${res.status} ${await res.text()}`)
  const { id } = (await res.json()) as { id: string }
  return `![${name}](omo-image:${id})`
}
