import type { Page } from '@playwright/test'
import { traceWheels, wheelTraceProblems } from './wheelTrace'

/** A scroll box a wheel can land on: where, which way it scrolls and how to name it. */
interface Target {
  index: number
  name: string
  x: number
  y: number
  axis: 'x' | 'y'
}

/**
 * Every visible element that is a scroll box (`overflow` auto or scroll) with
 * the point at which a wheel reaches it: near the centre of the part of it that
 * shows, where the element or something in it is on top. A point over a text
 * field is never used: the browser's own inner scroller of a text field always
 * needs the main thread to find its target, and no style changes that.
 */
async function targetsIn(page: Page, scope: string): Promise<Target[]> {
  return page.evaluate((scope) => {
    const out: { index: number; name: string; x: number; y: number; axis: 'x' | 'y' }[] = []
    const all = [...document.querySelectorAll<HTMLElement>(`${scope}, ${scope} *`)]
    all.forEach((el, index) => {
      const cs = getComputedStyle(el)
      const r = el.getBoundingClientRect()
      const left = Math.max(r.left, 0)
      const right = Math.min(r.right, innerWidth)
      const top = Math.max(r.top, 0)
      const bottom = Math.min(r.bottom, innerHeight)
      if (right - left < 24 || bottom - top < 24 || cs.visibility === 'hidden') return
      // Where a wheel reaches the box: near the middle, off any text field (see below).
      let found: { x: number; y: number } | null = null
      for (const [fx, fy] of [[0.5, 0.5], [0.5, 0.3], [0.5, 0.7], [0.3, 0.5], [0.7, 0.5], [0.3, 0.3], [0.7, 0.7], [0.3, 0.7], [0.7, 0.3], [0.5, 0.15], [0.5, 0.85]] as const) {
        const px = left + (right - left) * fx
        const py = top + (bottom - top) * fy
        const hit = document.elementFromPoint(px, py)
        if (hit && (el === hit || el.contains(hit)) && !hit.closest('input, textarea, select, [contenteditable], [role="textbox"]')) {
          found = { x: px, y: py }
          break
        }
      }
      if (!found) return
      const { x, y } = found
      const name = `${el.tagName.toLowerCase()}${[...el.classList].map((c) => `.${c}`).join('')}${el.getAttribute('data-testid') ? `[${el.getAttribute('data-testid')}]` : ''}`
      if (/auto|scroll/.test(cs.overflowY)) out.push({ index, name, x, y, axis: 'y' })
      if (/auto|scroll/.test(cs.overflowX)) out.push({ index, name, x, y, axis: 'x' })
    })
    return out
  }, scope)
}

/**
 * One trusted wheel over every scroll box inside `scope` (the page when
 * omitted), traced: the boxes whose wheel needed the main thread to find its
 * target, or was not scrolled by the compositor alone for another reason,
 * where a wheel can be dropped. Empty when every box passes. The wheel is
 * small, and each box is scrolled back, so the view stays as it was.
 */
export async function scrollBoxesTakingWheelsBadly(page: Page, scope = 'body', label = scope): Promise<string[]> {
  const bad: string[] = []
  const seen = new Set<string>()
  const targets = await targetsIn(page, scope)
  if (targets.length === 0) throw new Error(`nothing to scroll in ${label}: an audit with no scroll box proves nothing`)
  for (const t of targets) {
    const id = `${t.name} ${t.axis}`
    if (seen.has(id)) continue
    seen.add(id)
    await page.mouse.move(t.x, t.y)
    const trace = await traceWheels(page, async () => {
      if (t.axis === 'y') await page.mouse.wheel(0, 40)
      else await page.mouse.wheel(40, 0)
    })
    const problems = wheelTraceProblems(trace, 1)
    if (problems.length > 0) bad.push(`${id} at (${Math.round(t.x)}, ${Math.round(t.y)}): ${problems.join('; ')}`)
    await page.evaluate(
      ([scope, index]) => {
        const el = [...document.querySelectorAll<HTMLElement>(`${scope}, ${scope} *`)][index as number]
        if (el) {
          el.scrollTop = 0
          el.scrollLeft = 0
        }
      },
      [scope, t.index] as const,
    )
  }
  return bad
}
