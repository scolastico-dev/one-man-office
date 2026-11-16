import type { Page } from '@playwright/test'

/**
 * Controls that share a row must be equally tall. This finds the pairs that
 * are not.
 *
 * A control is a visible button, link drawn as a button, switch, select or
 * single-line input (not a checkbox, radio or text area). Two controls share
 * a row when their nearest common ancestor lays out a row (a flex or grid
 * container, or a table row) and is less than three times as tall as the
 * taller of the two (a line, not an area of the page such as a column or the
 * sidebar), and their boxes overlap vertically by at least half the shorter
 * one's height. A pair differs when their heights differ by more than 1 px.
 *
 * A control, or a container around it, may carry `data-height-free="<why>"`
 * to be left out: for the few places where a different height is meant.
 */
export interface RowMismatch {
  /** Where: the row container's tag, classes and data attributes. */
  row: string
  a: string
  b: string
  ha: number
  hb: number
}

/** `all`: every pair of controls sharing a row, equal or not (for a report). */
export function rowMismatches(page: Page, all = false): Promise<RowMismatch[]> {
  return page.evaluate((all) => {
    const CONTROL = [
      'button',
      'a.omo-btn',
      '[role="switch"]',
      'select',
      'input:not([type="checkbox"]):not([type="radio"]):not([type="hidden"]):not([type="range"]):not([type="color"]):not([type="file"])',
    ].join(',')
    const visible = (el: Element) => {
      if (el.closest('[data-height-free]')) return false
      const r = el.getBoundingClientRect()
      if (r.width < 2 || r.height < 2) return false
      const s = getComputedStyle(el)
      if (s.visibility === 'hidden' || s.display === 'none') return false
      if (s.clip === 'rect(0px, 0px, 0px, 0px)' || s.clipPath === 'inset(50%)') return false
      // Inside a closed details, an inert subtree or behind a hidden ancestor.
      return el.checkVisibility ? el.checkVisibility({ opacityProperty: false, visibilityProperty: true }) : true
    }
    const controls = [...document.querySelectorAll(CONTROL)].filter(visible)
    const ancestors = (el: Element) => {
      const out: Element[] = []
      for (let e: Element | null = el.parentElement; e; e = e.parentElement) out.push(e)
      return out
    }
    const common = (a: Element, b: Element) => {
      const of = new Set(ancestors(a))
      return ancestors(b).find((e) => of.has(e)) ?? null
    }
    const isRow = (el: Element) => {
      const d = getComputedStyle(el).display
      return /(^|\s)(flex|inline-flex|grid|inline-grid|table-row)$/.test(d)
    }
    const name = (el: Element) => {
      const label = el.getAttribute('aria-label') || (el as HTMLInputElement).placeholder || el.textContent?.trim().slice(0, 30) || ''
      return `${el.tagName.toLowerCase()}${el.className && typeof el.className === 'string' ? '.' + el.className.trim().split(/\s+/).join('.') : ''} "${label}"`
    }
    const where = (el: Element) => {
      const data = [...el.attributes].filter((a) => a.name.startsWith('data-') && !a.name.startsWith('data-v-')).map((a) => `[${a.name}]`)
      const cls = typeof el.className === 'string' && el.className.trim() ? '.' + el.className.trim().split(/\s+/).join('.') : ''
      return `${el.tagName.toLowerCase()}${cls}${data.join('')}`
    }
    const out: { row: string; a: string; b: string; ha: number; hb: number }[] = []
    const seen = new Set<string>()
    for (let i = 0; i < controls.length; i++) {
      for (let j = i + 1; j < controls.length; j++) {
        const a = controls[i]!
        const b = controls[j]!
        if (a.contains(b) || b.contains(a)) continue
        const ra = a.getBoundingClientRect()
        const rb = b.getBoundingClientRect()
        if (!all && Math.abs(ra.height - rb.height) <= 1) continue
        if (Math.min(ra.bottom, rb.bottom) - Math.max(ra.top, rb.top) < Math.min(ra.height, rb.height) / 2) continue
        const row = common(a, b)
        if (!row || !isRow(row)) continue
        if (row.getBoundingClientRect().height >= 3 * Math.max(ra.height, rb.height)) continue
        const key = `${where(row)}|${Math.round(ra.height)}|${Math.round(rb.height)}`
        if (seen.has(key)) continue
        seen.add(key)
        out.push({ row: where(row), a: name(a), b: name(b), ha: Math.round(ra.height * 10) / 10, hb: Math.round(rb.height * 10) / 10 })
      }
    }
    return out
  }, all)
}
