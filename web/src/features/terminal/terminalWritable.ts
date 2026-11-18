// Whether an agent terminal takes typing. Agent terminals start read-only: a
// slip of the keyboard or a stray paste must not reach the agent, and omo's
// own lines and the draft box need the input free of half-typed text. The
// choice is kept per terminal for the browser session; a terminal never
// seen starts read-only. Terminals that are not an agent's are always writable.
import { targetOfSrc } from './terminalTarget'

const KEY = 'omo.terminalWritable'

function read(): string[] {
  try {
    const parsed: unknown = JSON.parse(window.sessionStorage.getItem(KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : []
  } catch {
    return []
  }
}

/** Held in memory too: session storage may be blocked. */
const memory = new Set<string>()

/** Whether the terminal behind a websocket path is an agent's, and so has the read-only default. */
export function isAgentSrc(src: string): boolean {
  return targetOfSrc(src)?.kind === 'agent'
}

/** Whether the terminal takes typing right now. */
export function isWritable(src: string): boolean {
  const t = targetOfSrc(src)
  if (!t || t.kind !== 'agent') return true
  return memory.has(t.id) || read().includes(t.id)
}

export function setWritable(src: string, writable: boolean): void {
  const t = targetOfSrc(src)
  if (!t || t.kind !== 'agent') return
  if (writable) memory.add(t.id)
  else memory.delete(t.id)
  try {
    const ids = read().filter((id) => id !== t.id)
    if (writable) ids.push(t.id)
    window.sessionStorage.setItem(KEY, JSON.stringify(ids))
  } catch {
    // The choice then lasts as long as this page.
  }
}

/** For tests: forget every choice. */
export function resetWritable(): void {
  memory.clear()
  try {
    window.sessionStorage.removeItem(KEY)
  } catch {
    // Nothing kept.
  }
}
