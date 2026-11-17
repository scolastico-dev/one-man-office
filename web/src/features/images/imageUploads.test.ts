import { flushPromises } from '@vue/test-utils'
import { afterEach, describe, expect, test } from 'vitest'
import { ref } from 'vue'
import { ApiError } from '../../api/client'
import type { Image } from '../../api/types'
import { createImageUploads, MAX_IMAGE_BYTES } from './imageUploads'

const ID = '0f8fad5b-d9cb-469f-a165-70867728950e'

function image(name: string): Image {
  return {
    id: ID,
    ticket: 3,
    name,
    content_type: 'image/png',
    size: 3,
    author: { role: 'user' },
    created_at: '2026-11-16T10:00:00Z',
    state: 'stored',
    path: '/x',
    markdown: `![${name}](omo-image:${ID})`,
  }
}

let area: HTMLTextAreaElement | null = null
afterEach(() => {
  area?.remove()
  area = null
})

function setup(text: string, answer: (name: string) => Promise<Image>, caret?: number) {
  area = document.createElement('textarea')
  document.body.append(area)
  const model = ref(text)
  area.value = text
  if (caret !== undefined) {
    area.focus()
    area.setSelectionRange(caret, caret)
  }
  const sent: Array<{ name: string; size: number }> = []
  const said: string[] = []
  const u = createImageUploads({
    upload: (file, name) => {
      sent.push({ name, size: file.size })
      return answer(name)
    },
    text: model,
    area: () => area,
    announce: (m) => said.push(m),
  })
  return { u, model, sent, said }
}

const png = (name: string, size = 3) => new File([new Uint8Array(size)], name, { type: 'image/png' })

describe('image uploads into a text box', () => {
  test('a placeholder line shows at the caret while uploading, then becomes the reference', async () => {
    let finish!: (i: Image) => void
    const t = setup('before after', () => new Promise((r) => (finish = r)), 7)
    const done = t.u.add([png('shot.png')])
    expect(t.model.value).toMatch(/^before \n!\[Uploading shot\.png…\]\(#upload-\d+\)\nafter$/)
    expect(t.u.busy.value).toBe(1)
    finish(image('shot.png'))
    await done
    expect(t.model.value).toBe(`before \n![shot.png](omo-image:${ID})\nafter`)
    expect(t.u.busy.value).toBe(0)
    expect(t.u.errors.value).toEqual([])
    expect(t.sent).toEqual([{ name: 'shot.png', size: 3 }])
  })

  test('without focus the reference goes at the end; several files one after another', async () => {
    const t = setup('text', async (name) => image(name))
    await t.u.add([png('a.png'), png('b.png')])
    expect(t.model.value).toBe(`text\n![a.png](omo-image:${ID})\n![b.png](omo-image:${ID})\n`)
  })

  test("a refusal removes the placeholder, inserts nothing and shows the server's sentence", async () => {
    const t = setup('keep', () =>
      Promise.reject(new ApiError(422, 'validation', 'this file is not a PNG, JPEG, GIF or WebP image; omo takes only those (SVG is refused, it can carry scripts)')),
    )
    await t.u.add([new File(['<svg/>'], 'x.svg', { type: 'image/svg+xml' })])
    expect(t.model.value).toBe('keep')
    expect(t.u.errors.value).toEqual(['x.svg: this file is not a PNG, JPEG, GIF or WebP image; omo takes only those (SVG is refused, it can carry scripts)'])
    expect(t.said.at(-1)).toContain('x.svg: this file is not')
  })

  test('a file over 10 MiB is refused here and not sent', async () => {
    const t = setup('', async (name) => image(name))
    await t.u.add([png('big.png', MAX_IMAGE_BYTES + 1)])
    expect(t.sent).toEqual([])
    expect(t.model.value).toBe('')
    expect(t.u.errors.value[0]).toMatch(/^big\.png: an image has at most 10 MiB/)
  })

  test('a paste with image files uploads them instead of inserting text; a text paste is left alone', async () => {
    const t = setup('', async (name) => image(name))
    const files = [png('pasted.png')]
    let prevented = false
    t.u.onPaste({ clipboardData: { files } as unknown as DataTransfer, preventDefault: () => (prevented = true) } as unknown as ClipboardEvent)
    await flushPromises()
    expect(prevented).toBe(true)
    expect(t.model.value).toContain(`omo-image:${ID}`)
    let textPrevented = false
    t.u.onPaste({ clipboardData: { files: [] } as unknown as DataTransfer, preventDefault: () => (textPrevented = true) } as unknown as ClipboardEvent)
    expect(textPrevented).toBe(false)
  })

  test('a drop of files uploads them; a drop of text is left alone', async () => {
    const t = setup('', async (name) => image(name))
    let prevented = 0
    const drop = (files: File[], types: string[]) =>
      ({ dataTransfer: { files, types, dropEffect: 'none' }, target: area, preventDefault: () => prevented++ }) as unknown as DragEvent
    t.u.onDragOver(drop([], ['Files']))
    expect(prevented).toBe(1)
    t.u.onDragOver(drop([], ['text/plain']))
    expect(prevented).toBe(1)
    t.u.onDrop(drop([png('dropped.png')], ['Files']))
    await flushPromises()
    expect(prevented).toBe(2)
    expect(t.sent.map((s) => s.name)).toEqual(['dropped.png'])
  })
})
