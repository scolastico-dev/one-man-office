// The message drafted below an agent terminal: what is kept across a reload,
// and what is sent. The draft is the user's own; nothing but the user changes it.
import { cleanPaste } from './terminalPaste'
import { pastedPaths } from './terminalImages'

/** An image stored by omo and attached to a draft; its path is what the program gets. */
export interface Attachment {
  path: string
  name: string
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
      const attachments = Array.isArray(v.attachments)
        ? v.attachments.filter((a): a is Attachment => !!a && typeof (a as Attachment).path === 'string' && typeof (a as Attachment).name === 'string')
        : []
      out[id] = { text: v.text, attachments: attachments.map((a) => ({ path: a.path, name: a.name })), at: typeof v.at === 'number' ? v.at : Date.now() }
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

/** The message a draft sends: its text, cleaned like a paste, then the attached images' paths on a line of their own. Empty when there is nothing to send. */
export function messageOf(text: string, paths: readonly string[]): string {
  const body = cleanPaste(text)
  const files = pastedPaths(paths)
  const message = body.trim() === '' ? files : files === '' ? body : `${body}\n${files}`
  return message.trim() === '' ? '' : message
}
