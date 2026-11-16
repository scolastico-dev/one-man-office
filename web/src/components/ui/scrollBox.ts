// Brings an element into view inside the box that scrolls it, and nowhere
// else. The browser's own scrollIntoView moves every scrollable ancestor up
// to the document, boxes with hidden overflow included: in the board's frame
// that could move the whole page or a pane that is meant to stay put. Every
// "show this row" of the app goes through revealInBox instead (a guard test
// keeps it so).

export type RevealBlock = 'nearest' | 'start' | 'center'

/** The nearest ancestor of `el` that scrolls (vertical overflow auto or scroll) and has more to show than fits; null when none does. */
export function scrollBoxOf(el: Element): HTMLElement | null {
  for (let p = el.parentElement; p !== null && p !== document.body && p !== document.documentElement; p = p.parentElement) {
    const y = getComputedStyle(p).overflowY
    if ((y === 'auto' || y === 'scroll') && p.scrollHeight > p.clientHeight) return p
  }
  return null
}

/**
 * Scrolls the nearest box that scrolls `el` so that `el` shows: `nearest`
 * moves as little as possible (not at all when it shows already), `start`
 * puts it at the box's top, `center` in its middle. Nothing else moves.
 */
export function revealInBox(el: Element, block: RevealBlock = 'nearest'): void {
  const box = scrollBoxOf(el)
  if (box === null) return
  const outer = box.getBoundingClientRect()
  const r = el.getBoundingClientRect()
  // The visible part of the box, inside its border.
  const viewTop = outer.top + box.clientTop
  const viewHeight = box.clientHeight
  const above = r.top - viewTop
  const below = r.bottom - (viewTop + viewHeight)
  let by = 0
  if (block === 'start') by = above
  else if (block === 'center') by = above - (viewHeight - r.height) / 2
  else if (above < 0 || r.height > viewHeight) by = above
  else if (below > 0) by = below
  if (by !== 0) box.scrollTop += by
}
