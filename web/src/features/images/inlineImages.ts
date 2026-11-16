// The pictures of uploaded images in rendered Markdown. An image's bytes need
// the login token, which a plain <img src> cannot send and which must never
// go into an address; so the record is read first (stored, expired or
// missing), then the bytes with the API client, and the picture is shown
// through an object URL. Every object URL is released when the text stops
// referring to its image, when the image is deleted or expires, and when
// the view goes away.
import { getCurrentScope, onScopeDispose, shallowReactive, watch } from 'vue'
import { ApiError, type ApiClient } from '../../api/client'
import { getImage, getImageBytes } from '../../api/endpoints'
import type { ApiEvent, ImageEventData } from '../../api/types'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { imageRefs, type ImageView } from '../ticket/markdown'

export interface InlineImagesDeps {
  client: ApiClient
  onEvent(kind: string, fn: (ev: ApiEvent) => void): () => void
  createObjectURL?: (b: Blob) => string
  revokeObjectURL?: (url: string) => void
}

export interface InlineImages {
  /** What image `id` shows now; reactive. For `renderMarkdown`'s `images`. */
  view(id: string): ImageView
  /** Loads the images `text` refers to and releases those it no longer does. */
  follow(text: string): void
  /** Releases every object URL and stops listening; the views stay empty. */
  release(): void
}

const LOADING: ImageView = { state: 'loading' }

export function createInlineImages(deps: InlineImagesDeps): InlineImages {
  const create = deps.createObjectURL ?? ((b: Blob) => URL.createObjectURL(b))
  const revoke = deps.revokeObjectURL ?? ((u: string) => URL.revokeObjectURL(u))
  const views = shallowReactive(new Map<string, ImageView>())
  /** The newest load of an id; an older answer is dropped. */
  const loads = new Map<string, number>()
  let wanted = new Set<string>()
  let next = 0
  let released = false

  function drop(v: ImageView | undefined) {
    if (v?.state === 'shown') revoke(v.url)
  }

  function set(id: string, v: ImageView) {
    drop(views.get(id))
    views.set(id, v)
  }

  function forget(id: string) {
    drop(views.get(id))
    views.delete(id)
    loads.delete(id)
  }

  function settle(id: string, load: number, v: ImageView) {
    if (released || loads.get(id) !== load || !wanted.has(id)) {
      drop(v)
      return
    }
    loads.delete(id)
    set(id, v)
  }

  async function load(id: string) {
    const mine = ++next
    loads.set(id, mine)
    set(id, LOADING)
    try {
      const { data } = await getImage(deps.client, id)
      if (data.state !== 'stored') {
        settle(id, mine, { state: data.state === 'expired' ? 'expired' : 'missing' })
        return
      }
      const blob = await getImageBytes(deps.client, id)
      if (released || loads.get(id) !== mine) return
      settle(id, mine, { state: 'shown', url: create(blob) })
    } catch (e) {
      const gone = e instanceof ApiError && (e.status === 404 || e.status === 403)
      settle(id, mine, { state: gone ? 'missing' : 'failed' })
    }
  }

  const offs = [
    deps.onEvent('image.deleted', (ev) => {
      const id = (ev.data as ImageEventData).id
      if (wanted.has(id)) {
        loads.delete(id)
        set(id, { state: 'missing' })
      }
    }),
    deps.onEvent('image.expired', (ev) => {
      const id = (ev.data as ImageEventData).id
      if (wanted.has(id)) {
        loads.delete(id)
        set(id, { state: 'expired' })
      }
    }),
    deps.onEvent('image.added', (ev) => {
      const id = (ev.data as ImageEventData).id
      const v = views.get(id)
      if (wanted.has(id) && (v?.state === 'missing' || v?.state === 'failed')) void load(id)
    }),
  ]

  return {
    view: (id) => views.get(id) ?? LOADING,
    follow(text) {
      if (released) return
      const ids = new Set(imageRefs(text))
      for (const id of wanted) if (!ids.has(id)) forget(id)
      wanted = ids
      for (const id of ids) if (!views.has(id)) void load(id)
    },
    release() {
      if (released) return
      released = true
      for (const off of offs) off()
      for (const v of views.values()) drop(v)
      views.clear()
      loads.clear()
      wanted = new Set()
    },
  }
}

/** The images of the Markdown `text` of a view; released with the view. */
export function useInlineImages(text: () => string): InlineImages {
  const session = useSession()
  const store = useBoard()
  const images = createInlineImages({ client: session.client, onEvent: (k, fn) => store.onEvent(k, fn) })
  watch(text, (t) => images.follow(t), { immediate: true })
  if (getCurrentScope()) onScopeDispose(() => images.release())
  return images
}
