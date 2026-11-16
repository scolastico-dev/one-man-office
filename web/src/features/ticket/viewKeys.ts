// The single-letter keys of a ticket's views: `p` switches between the panel
// and the ticket's page, `b` goes from the page back to the board. Like the
// board's `c` and `/`, they do nothing while the user types in a field or a
// terminal, or with a modifier held.

/** The key that opens the ticket's page from its panel, and its panel from its page. */
export const SWITCH_VIEW_KEY = 'p'
/** The key that goes from a ticket's page back to the board. */
export const BACK_TO_BOARD_KEY = 'b'

/** Whether a key press belongs to something the user is typing in. */
export function typing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return true
  // A terminal takes every key.
  return target.closest('.xterm') !== null
}

/** Whether `e` is the plain key `key`, pressed outside any field. */
export function plainKey(e: KeyboardEvent, key: string): boolean {
  if (e.key !== key || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return false
  return !e.defaultPrevented && !e.isComposing && !typing(e.target)
}
