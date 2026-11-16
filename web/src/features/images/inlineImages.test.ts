import { flushPromises } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import { ApiError, type ApiClient } from '../../api/client'
import type { ApiEvent, Image } from '../../api/types'
import { createInlineImages } from './inlineImages'

const A = '0f8fad5b-d9cb-469f-a165-70867728950e'
const B = '7c9e6679-7425-40de-944b-e07fc1f90ae7'
const ref = (id: string, alt = 'x.png') => `![${alt}](omo-image:${id})`

function record(id: string, state: Image['state']): Image {
  return {
    id,
    ticket: 1,
    name: 'x.png',
    content_type: 'image/png',
    size: 3,
    author: { role: 'user' },
    created_at: '2026-11-16T10:00:00Z',
    state,
    path: state === 'stored' ? `/home/u/.local/omo/images/${id}.png` : '',
    markdown: ref(id),
  }
}

function setup(records: Record<string, Image['state'] | 'network' | 'forbidden'>) {
  const reads: string[] = []
  const handlers = new Map<string, Array<(ev: ApiEvent) => void>>()
  const created: string[] = []
  const revoked: string[] = []
  const client = {
    get: async (path: string) => {
      reads.push(path)
      const id = path.split('/').pop()!
      const r = records[id]
      if (r === 'network') throw new ApiError(0, 'network', 'down')
      if (r === 'forbidden') throw new ApiError(403, 'forbidden', 'no')
      if (r === undefined) throw new ApiError(404, 'not_found', 'no such image')
      return { data: record(id, r), eventId: null }
    },
    getBlob: async (path: string) => {
      reads.push(path)
      return new Blob(['png'])
    },
  } as unknown as ApiClient
  let n = 0
  const images = createInlineImages({
    client,
    onEvent: (kind, fn) => {
      handlers.set(kind, [...(handlers.get(kind) ?? []), fn])
      return () => handlers.set(kind, (handlers.get(kind) ?? []).filter((f) => f !== fn))
    },
    createObjectURL: () => {
      const url = `blob:http://omo.localhost/${++n}`
      created.push(url)
      return url
    },
    revokeObjectURL: (u) => revoked.push(u),
  })
  const emit = (kind: string, id: string) => {
    for (const fn of handlers.get(kind) ?? []) fn({ id: 1, kind, ticket: 1, actor: { role: 'user' }, at: '', data: { ticket: 1, id, name: 'x.png', size: 3 } })
  }
  return { images, reads, created, revoked, emit, handlers }
}

describe('inline images', () => {
  test('a stored image is read with the API client and shown through an object URL', async () => {
    const t = setup({ [A]: 'stored' })
    t.images.follow(`one ${ref(A)} two ${ref(A)}`)
    expect(t.images.view(A)).toEqual({ state: 'loading' })
    await flushPromises()
    expect(t.images.view(A)).toEqual({ state: 'shown', url: 'blob:http://omo.localhost/1' })
    expect(t.reads).toEqual([`/api/images/${A}`, `/api/images/${A}/content`])
  })

  test('expired, missing, refused and failed images say so and fetch no bytes', async () => {
    const t = setup({ [A]: 'expired', [B]: 'missing' })
    const C = '1b4e28ba-2fa1-41d2-883f-0016d3cca427'
    const D = '6ba7b810-9dad-41d1-80b4-00c04fd430c8'
    const t2 = setup({ [C]: 'network', [D]: 'forbidden' })
    t.images.follow(ref(A) + ref(B) + ref('9a1c2b3d-0000-4000-8000-000000000000'))
    t2.images.follow(ref(C) + ref(D))
    await flushPromises()
    expect(t.images.view(A).state).toBe('expired')
    expect(t.images.view(B).state).toBe('missing')
    expect(t.images.view('9a1c2b3d-0000-4000-8000-000000000000').state).toBe('missing')
    expect(t2.images.view(C).state).toBe('failed')
    expect(t2.images.view(D).state).toBe('missing')
    expect(t.reads.some((p) => p.endsWith('/content'))).toBe(false)
    expect(t.created).toEqual([])
  })

  test('object URLs are released when the text drops the image, on deletion, on expiry and when the view goes away', async () => {
    const t = setup({ [A]: 'stored', [B]: 'stored' })
    t.images.follow(ref(A) + ref(B))
    await flushPromises()
    expect(t.created).toHaveLength(2)

    t.images.follow(ref(B))
    expect(t.revoked).toEqual(['blob:http://omo.localhost/1'])

    t.emit('image.deleted', B)
    expect(t.revoked).toEqual(['blob:http://omo.localhost/1', 'blob:http://omo.localhost/2'])
    expect(t.images.view(B).state).toBe('missing')

    t.images.follow(ref(A))
    await flushPromises()
    t.emit('image.expired', A)
    expect(t.images.view(A).state).toBe('expired')
    expect(t.revoked).toContain('blob:http://omo.localhost/3')

    t.images.follow(ref(B, 'again'))
    await flushPromises()
    t.images.release()
    expect(new Set(t.revoked)).toEqual(new Set(t.created))
    expect([...t.handlers.values()].every((fns) => fns.length === 0)).toBe(true)
  })

  test('an answer that arrives after the view went away creates nothing that stays', async () => {
    const t = setup({ [A]: 'stored' })
    t.images.follow(ref(A))
    t.images.release()
    await flushPromises()
    expect(t.created).toEqual([])
  })

  test('an image added after it was missing is loaded again', async () => {
    const records: Record<string, Image['state']> = {}
    const t = setup(records)
    t.images.follow(ref(A))
    await flushPromises()
    expect(t.images.view(A).state).toBe('missing')
    records[A] = 'stored'
    t.emit('image.added', A)
    await flushPromises()
    expect(t.images.view(A).state).toBe('shown')
  })
})
