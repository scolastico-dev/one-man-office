// The message drafted below an agent terminal: what is kept across a reload,
// and what is sent. The draft is the user's own; nothing but the user changes it.
import { cleanPaste } from './terminalPaste'
import { pastedPaths } from './terminalImages'
import type { StoredImage } from './terminalUploads'

/** An image stored by omo and attached to a draft; its path is what the program gets. */
export interface Attachment {
  path: string
  name: string
  at: number
  marker: string
}

export interface Draft {
  text: string
  attachments: Attachment[]
}

const KEY = 'omo.terminalDrafts'
const KEEP_MS = 14 * 86_400_000

/**
 * The longest message, in characters. The server takes 64 KiB of bytes per
 * message and a character is at most four of them.
 */
export const MAX_MESSAGE_CHARS = 12_000

interface Stored extends Draft {
  at: number
}

function readAll(): Record<string, Stored> {
  try {
    const raw = window.localStorage.getItem(KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : {}
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return {}
    const out: Record<string, Stored> = {}
    for (const [id, value] of Object.entries(parsed as Record<string, unknown>)) {
      const v = value as { text?: unknown; attachments?: unknown; at?: unknown } | null
      if (!v || typeof v.text !== 'string') continue
      let draft: Draft = { text: v.text, attachments: [] }
      if (Array.isArray(v.attachments)) for (const item of v.attachments) {
        if (!item || typeof item.path !== 'string' || typeof item.name !== 'string' || !item.path) continue
        if (item.at === undefined && item.marker === undefined) {
          // Drafts saved before inline markers kept images after the text.
          draft = insertImage(draft, draft.text.length, draft.text.length, item)
        } else if (Number.isInteger(item.at) && item.at >= 0 && typeof item.marker === 'string' && /^\[image [1-9]\d*\]$/.test(item.marker)
          && draft.text.slice(item.at, item.at + item.marker.length) === item.marker
          && !draft.attachments.some((a) => a.marker === item.marker || (a.at < item.at + item.marker.length && item.at < a.at + a.marker.length))) {
          draft.attachments.push({ path: item.path, name: item.name, at: item.at, marker: item.marker })
        }
      }
      draft.attachments.sort((a, b) => a.at - b.at)
      out[id] = { ...draft, at: typeof v.at === 'number' ? v.at : Date.now() }
    }
    return out
  } catch {
    return {}
  }
}

function writeAll(all: Record<string, Stored>): void {
  try {
    if (Object.keys(all).length === 0) window.localStorage.removeItem(KEY)
    else window.localStorage.setItem(KEY, JSON.stringify(all))
  } catch {
    // Blocked or full storage: the draft stays in the box only.
  }
}

/** The draft kept for a terminal; empty when there is none. */
export function loadDraft(id: string): Draft {
  const d = readAll()[id]
  return d ? { text: d.text, attachments: d.attachments } : { text: '', attachments: [] }
}

/** Keeps the draft of a terminal; an empty one is removed. Drafts of terminals long gone are dropped with it. */
export function saveDraft(id: string, draft: Draft): void {
  // Upload placeholders have no stored image yet; do not restore them on reload.
  for (const a of [...draft.attachments].reverse()) {
    if (!a.path) draft = editDraft(draft, a.at, a.at + a.marker.length, '')
  }
  const all = readAll()
  const now = Date.now()
  for (const [key, d] of Object.entries(all)) if (now - d.at > KEEP_MS) delete all[key]
  if (draft.text === '' && draft.attachments.length === 0) delete all[id]
  else all[id] = { ...draft, at: now }
  writeAll(all)
}

export function clearDraft(id: string): void {
  saveDraft(id, { text: '', attachments: [] })
}

/** Inserts after a marker when the caret is inside it; expands selected ranges to whole markers. */
export function markerRange(draft: Draft, start: number, end: number): [number, number] {
  for (const a of draft.attachments) {
    const limit = a.at + a.marker.length
    if (start === end && start > a.at && start < limit) return [limit, limit]
    if (start < limit && end > a.at) {
      start = Math.min(start, a.at)
      end = Math.max(end, limit)
    }
  }
  return [start, end]
}

/** One text edit, with markers treated as indivisible attachments. */
export function editDraft(draft: Draft, start: number, end: number, replacement: string): Draft {
  ;[start, end] = markerRange(draft, start, end)
  const shift = replacement.length - (end - start)
  return {
    text: draft.text.slice(0, start) + replacement + draft.text.slice(end),
    attachments: draft.attachments.filter((a) => a.at + a.marker.length <= start || a.at >= end)
      .map((a) => ({ ...a, at: a.at >= end ? a.at + shift : a.at })),
  }
}

export function nextImageNumber(draft: Draft): number {
  return Math.max(0, ...draft.attachments.map((a) => Number(a.marker.slice(7, -1)))) + 1
}

/** Inserts an image marker at the paste selection; its path is never shown. */
export function insertImage(draft: Draft, start: number, end: number, image: StoredImage, number = nextImageNumber(draft)): Draft {
  ;[start, end] = markerRange(draft, start, end)
  const marker = `[image ${number}]`
  const edited = editDraft(draft, start, end, marker)
  edited.attachments.push({ ...image, at: start, marker })
  edited.attachments.sort((a, b) => a.at - b.at)
  return edited
}

/** Ordered, independently pasted text and image paths. The server submits once at the end. */
export function messageOf(text: string, attachments: readonly Attachment[]): string[] {
  const parts: string[] = []
  let at = 0
  for (const a of attachments) {
    const before = cleanPaste(text.slice(at, a.at))
    if (before) parts.push(before)
    parts.push(cleanPaste(pastedPaths([a.path])))
    at = a.at + a.marker.length
  }
  const tail = cleanPaste(text.slice(at))
  if (tail) parts.push(tail)
  return parts.join('').trim() === '' ? [] : parts
}
