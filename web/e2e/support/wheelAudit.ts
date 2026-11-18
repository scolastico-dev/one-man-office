import type { Page } from '@playwright/test'
import { traceWheels, wheelTraceProblems } from './wheelTrace'

/** What an audit did: the boxes that took a wheel badly, and every box and axis it turned a wheel over. */
export interface WheelAudit {
  bad: string[]
  visited: string[]
}

const MARK = 'data-wheel-audit'

/**
 * Marks every scroll box (`overflow` auto or scroll, in either axis) inside
 * `scope` that has room to show a wheel and returns what to turn a wheel over.
 * The marks are the only handle kept: where to point is worked out again for
 * each wheel, because scrolling one box can move the others.
 */
async function mark(page: Page, scope: string): Promise<{ id: number; name: string; axis: 'x' | 'y' }[]> {
  return page.evaluate(
    ([scope, mark]) => {
      const out: { id: number; name: string; axis: 'x' | 'y' }[] = []
      let id = 0
      for (const el of document.querySelectorAll<HTMLElement>(`${scope}, ${scope} *`)) {
        // A text field's own scroller is never a target (see pointOn), nor is a terminal's: its wheel is xterm.js's own listener.
        if (el.matches('input, textarea, select, [contenteditable], [role="textbox"]') || el.closest('.xterm')) continue
        const cs = getComputedStyle(el)
        const r = el.getBoundingClientRect()
        if (cs.visibility === 'hidden' || r.width < 12 || r.height < 12) continue
        const axes: ('x' | 'y')[] = []
        if (/auto|scroll/.test(cs.overflowY)) axes.push('y')
        if (/auto|scroll/.test(cs.overflowX)) axes.push('x')
        if (axes.length === 0) continue
        el.setAttribute(mark, String(id))
        const name = `${el.tagName.toLowerCase()}${[...el.classList].map((c) => `.${c}`).join('')}`
        for (const axis of axes) out.push({ id, name, axis })
        id++
      }
      return out
    },
    [scope, MARK] as const,
  )
}

/**
 * The scroll offsets of every element of the scope and of everything around it
 * (each ancestor of the scope and the document), kept on the window: a wheel
 * that a box hands on moves the box around it, which may lie outside the scope.
 */
async function snapshot(page: Page, scope: string): Promise<void> {
  await page.evaluate((scope) => {
    const kept = new Set<Element>(document.querySelectorAll<HTMLElement>(`${scope}, ${scope} *`))
    for (const el of [...kept]) for (let up = el.parentElement; up; up = up.parentElement) kept.add(up)
    kept.add(document.scrollingElement ?? document.documentElement)
    ;(window as unknown as { __wheelAuditOffsets: [Element, number, number][] }).__wheelAuditOffsets = [...kept].map((el) => [el, (el as HTMLElement).scrollTop, (el as HTMLElement).scrollLeft])
  }, scope)
}

/** Puts every scroll offset back to the snapshot and waits until each one is exactly there and a frame has shown it. */
async function restore(page: Page): Promise<void> {
  await page.evaluate(() => {
    for (const [el, top, left] of (window as unknown as { __wheelAuditOffsets: [HTMLElement, number, number][] }).__wheelAuditOffsets) {
      if (el.scrollTop !== top) el.scrollTop = top
      if (el.scrollLeft !== left) el.scrollLeft = left
    }
  })
  await page.waitForFunction(() => (window as unknown as { __wheelAuditOffsets: [HTMLElement, number, number][] }).__wheelAuditOffsets.every(([el, top, left]) => el.scrollTop === top && el.scrollLeft === left))
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
}

/**
 * The point at which a wheel reaches the marked box now (the box is scrolled
 * into view when it lies outside the window): near the middle of the part of it that shows, where the box or something in it is on top, and
 * never over a text field (the browser's own inner scroller of a text field
 * always needs the main thread to find its target, and no style changes that).
 */
async function pointOn(page: Page, id: number): Promise<{ x: number; y: number } | null> {
  return page.evaluate(
    ([id, mark]) => {
      const el = document.querySelector<HTMLElement>(`[${mark}="${id}"]`)
      if (!el) return null
      // A box below the fold is brought into view first; the next wheel's restore puts every offset back.
      let r = el.getBoundingClientRect()
      if (r.bottom < 12 || r.top > innerHeight - 12 || r.right < 12 || r.left > innerWidth - 12) {
        el.scrollIntoView({ block: 'center', inline: 'center' })
        r = el.getBoundingClientRect()
      }
      const left = Math.max(r.left, 0)
      const right = Math.min(r.right, innerWidth)
      const top = Math.max(r.top, 0)
      const bottom = Math.min(r.bottom, innerHeight)
      for (const [fx, fy] of [[0.5, 0.5], [0.5, 0.3], [0.5, 0.7], [0.3, 0.5], [0.7, 0.5], [0.3, 0.3], [0.7, 0.7], [0.3, 0.7], [0.7, 0.3], [0.5, 0.15], [0.5, 0.85]] as const) {
        const x = left + (right - left) * fx
        const y = top + (bottom - top) * fy
        const hit = document.elementFromPoint(x, y)
        if (hit && (el === hit || el.contains(hit)) && !hit.closest('input, textarea, select, [contenteditable], [role="textbox"]')) return { x, y }
      }
      return null
    },
    [id, MARK] as const,
  )
}

/**
 * One trusted wheel over every visible scroll box inside `scope` (the page
 * when omitted), traced: the boxes whose wheel needed the main thread to find
 * its target, or was not scrolled by the compositor alone for another reason,
 * where a wheel can be dropped. Before each wheel every scroll offset of the
 * scope is back where it was at the start, and the point is worked out again
 * and checked, so each wheel really lands on its box even when an earlier one
 * handed its scroll to a box around it. Throws when the scope has no scroll
 * box, as an audit of nothing proves nothing.
 */
export async function auditWheels(page: Page, scope = 'body', label = scope): Promise<WheelAudit> {
  const boxes = await mark(page, scope)
  if (boxes.length === 0) throw new Error(`nothing to scroll in ${label}: an audit with no scroll box proves nothing`)
  await snapshot(page, scope)
  const audit: WheelAudit = { bad: [], visited: [] }
  try {
    for (const box of boxes) {
      const id = `${box.name} ${box.axis}`
      await restore(page)
      const point = await pointOn(page, box.id)
      if (!point) {
        audit.bad.push(`${id}: no point of it shows a wheel, away from text fields`)
        continue
      }
      await page.mouse.move(point.x, point.y)
      const trace = await traceWheels(page, async () => {
        if (box.axis === 'y') await page.mouse.wheel(0, 40)
        else await page.mouse.wheel(40, 0)
      })
      audit.visited.push(id)
      const problems = wheelTraceProblems(trace, 1)
      if (problems.length > 0) audit.bad.push(`${id} at (${Math.round(point.x)}, ${Math.round(point.y)}): ${problems.join('; ')}`)
    }
  } finally {
    await restore(page)
    await page.evaluate((mark) => document.querySelectorAll(`[${mark}]`).forEach((el) => el.removeAttribute(mark)), MARK)
  }
  return audit
}

/** The boxes of `auditWheels` that took a wheel badly; empty when every box passes. */
export async function scrollBoxesTakingWheelsBadly(page: Page, scope = 'body', label = scope): Promise<string[]> {
  return (await auditWheels(page, scope, label)).bad
}
