// The app's single-key shortcuts (`c`, `/`, a ticket view's `p` and `b`)
// share one rule: they do nothing while the user types in a field or a
// terminal, with Ctrl, Alt or Meta held, or while a modal dialog is open. A
// dialog that is not modal (the ticket panel on a wide screen) leaves them
// working. `shortcuts.guard.test.ts` fails on a second copy of these checks.

/** Whether a key press belongs to something the user is typing in. */
export function typing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return true
  // A terminal takes every key.
  return target.closest('.xterm') !== null
}

/** Whether a modal dialog other than `except` is open: everything outside it is inert. */
export function modalOpen(except: Element | null = null): boolean {
  for (const d of document.querySelectorAll('dialog[open]')) if (d !== except && d.matches(':modal')) return true
  return false
}

/**
 * Whether `e` is the plain key `key`, pressed outside any field. Shift is
 * allowed: it already changes a letter, and some layouts need it for `/`.
 */
export function plainKey(e: KeyboardEvent, key: string): boolean {
  if (e.key !== key || e.ctrlKey || e.metaKey || e.altKey) return false
  return !e.defaultPrevented && !e.isComposing && !typing(e.target)
}

/** A shortcut of the whole page: the plain key, while no modal dialog is open. */
export function pageKey(e: KeyboardEvent, key: string): boolean {
  return plainKey(e, key) && !modalOpen()
}
