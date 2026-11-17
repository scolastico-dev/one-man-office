// Dragging cards with the mouse, a pen or a finger, through pointer events.
// The drag only finds out where the pointer is; what a drop there means comes
// from `targets`, and the drop itself goes to `drop`.
import { readonly, ref, shallowRef, type Ref } from 'vue'
import type { Column, TicketSummary } from '../../api/types'
import { COLUMNS } from '../../api/types'
import { beforeFor } from '../../board/ordering'
import type { Target } from '../../board/moveRules'
import { placesCard, type CancelReason, type MoveOptions } from './useMoves'

/** The drag under way. */
export interface DragActive {
  n: number
  /** The column under the pointer (its panel, or its tab on narrow screens). */
  over: Column | null
  /** Where the card would land among the other cards of its own group in `over`, 0 the top. */
  index: number
  /** The `before` a drop now sends. */
  before: number | null
  /** The shown card the drop line is drawn above; null: below the last shown card of the group. */
  marker: number | null
}

export interface DragOptions extends MoveOptions {
  pickedUp?(n: number): void
  cancelled?(n: number, why: CancelReason, target?: Target): void
  /** Reduced motion: scroll near edges in steps instead of smoothly. */
  reducedMotion?(): boolean
  /** Element under a point; `document.elementFromPoint` by default. */
  hitTest?(x: number, y: number): Element | null
}

export interface Drag {
  active: Readonly<Ref<DragActive | null>>
  /** Size of the card being dragged, for the card that follows the pointer. */
  ghost: Readonly<Ref<{ n: number; width: number } | null>>
  /** The element that follows the pointer; null when it goes. */
  setGhost(el: HTMLElement | null): void
  onPointerDown(e: PointerEvent, n: number): void
  /** Ends the drag without a drop. */
  cancel(why?: CancelReason): void
  /** Finds the drop place again, for when the cards changed under a still pointer. */
  refresh(): void
}

/** Mouse and pen: movement before a press becomes a drag. */
export const DRAG_THRESHOLD_PX = 4
/** Touch: how long a finger rests on a card before it is picked up. */
export const LONG_PRESS_MS = 300
/** Touch: movement during the long press that makes it a scroll instead. */
const TOUCH_SLOP_PX = 8
/** Distance from an edge of a scrolling area where it scrolls by itself. */
const EDGE_PX = 48
const MAX_SPEED_PX = 18
/** Reduced motion: one step of a page, at most this often. */
const STEP_EVERY_MS = 600

/** Parts of a card that keep their own pointer behaviour. */
const NO_DRAG = 'button, input, textarea, select, label, [contenteditable="true"], a[href]:not([data-card-link]), [data-no-drag]'

interface Gesture {
  pointerId: number
  type: string
  n: number
  source: HTMLElement
  root: HTMLElement
  startX: number
  startY: number
  x: number
  y: number
  offsetX: number
  offsetY: number
  started: boolean
  timer: ReturnType<typeof setTimeout> | null
  frame: number | null
  lastStep: number
  saved: { userSelect: string; cursor: string } | null
}

function isColumn(v: string | undefined): v is Column {
  return v !== undefined && (COLUMNS as readonly string[]).includes(v)
}

function scrolls(el: Element, axis: 'x' | 'y'): boolean {
  const style = getComputedStyle(el)
  const overflow = axis === 'x' ? style.overflowX : style.overflowY
  if (overflow !== 'auto' && overflow !== 'scroll') return false
  return axis === 'x' ? el.scrollWidth > el.clientWidth : el.scrollHeight > el.clientHeight
}

/** The nearest area around `el` that scrolls along `axis`, else the page. */
function scroller(el: Element | null, axis: 'x' | 'y'): Element | null {
  for (let e = el; e !== null && e !== document.body && e !== document.documentElement; e = e.parentElement) {
    if (scrolls(e, axis)) return e
  }
  const page = document.scrollingElement
  if (page === null) return null
  return (axis === 'x' ? page.scrollWidth > page.clientWidth : page.scrollHeight > page.clientHeight) ? page : null
}

/** How far to scroll along one axis for a pointer at `p` in [lo, hi]: negative near lo, positive near hi. */
function push(p: number, lo: number, hi: number): number {
  const edge = Math.min(EDGE_PX, (hi - lo) / 4)
  if (p < lo + edge) return -Math.min(1, (lo + edge - p) / edge)
  if (p > hi - edge) return Math.min(1, (p - (hi - edge)) / edge)
  return 0
}

function visibleBox(el: Element): { left: number; top: number; right: number; bottom: number } {
  if (el === document.scrollingElement) return { left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight }
  const r = el.getBoundingClientRect()
  return { left: Math.max(r.left, 0), top: Math.max(r.top, 0), right: Math.min(r.right, window.innerWidth), bottom: Math.min(r.bottom, window.innerHeight) }
}

export function useDrag(o: DragOptions): Drag {
  const active = ref<DragActive | null>(null)
  const ghost = shallowRef<{ n: number; width: number } | null>(null)
  let ghostEl: HTMLElement | null = null
  let g: Gesture | null = null
  const hitTest = o.hitTest ?? ((x: number, y: number) => document.elementFromPoint(x, y))
  const reduced = o.reducedMotion ?? (() => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches)

  function ticket(n: number): TicketSummary | undefined {
    for (const c of COLUMNS) {
      const t = o.order(c).find((x) => x.number === n)
      if (t !== undefined) return t
    }
    return undefined
  }

  function place(): void {
    if (g === null || ghostEl === null) return
    ghostEl.style.transform = `translate3d(${g.x - g.offsetX}px, ${g.y - g.offsetY}px, 0)`
  }

  function setGhost(el: HTMLElement | null): void {
    ghostEl = el
    place()
  }

  /** Where a drop at the pointer would go. */
  function locate(): DragActive | null {
    if (g === null) return null
    const n = g.n
    const zone = hitTest(g.x, g.y)?.closest<HTMLElement>('[data-drop-column]') ?? null
    const column = zone?.dataset.dropColumn
    if (zone === null || !isColumn(column)) return { n, over: null, index: 0, before: null, marker: null }
    const target = o.targets(n)[column]
    const dragged = ticket(n)
    if (dragged === undefined || !placesCard(target)) return { n, over: column, index: 0, before: null, marker: null }
    const full = o.order(column)
    const group = full.filter((t) => t.type === dragged.type && t.number !== n)
    const inGroup = new Map(group.map((t, i) => [t.number, i]))
    let index = group.length
    let marker: number | null = null
    // A column's tab on a narrow screen puts the card at the end of its group.
    if (zone.tagName === 'SECTION') {
      const shown = [...zone.querySelectorAll<HTMLElement>('li[data-ticket]')]
        .map((li) => ({ li, n: Number(li.dataset.ticket) }))
        .filter((c) => inGroup.has(c.n))
      const below = shown.find((c) => {
        const r = c.li.getBoundingClientRect()
        return g!.y < r.top + r.height / 2
      })
      if (below !== undefined) {
        index = inGroup.get(below.n)!
        marker = below.n
      } else {
        const last = shown[shown.length - 1]
        if (last !== undefined) index = inGroup.get(last.n)! + 1
      }
    }
    return { n, over: column, index, before: beforeFor(full, dragged, index, o.unanswered), marker }
  }

  function update(): void {
    const next = locate()
    const cur = active.value
    if (next === null || cur === null) return
    if (cur.over !== next.over || cur.index !== next.index || cur.before !== next.before || cur.marker !== next.marker) active.value = next
  }

  function autoScroll(now: number): boolean {
    if (g === null) return false
    const under = hitTest(g.x, g.y)
    let moved = false
    for (const axis of ['x', 'y'] as const) {
      const el = scroller(under, axis)
      if (el === null) continue
      const box = visibleBox(el)
      const f = axis === 'x' ? push(g.x, box.left, box.right) : push(g.y, box.top, box.bottom)
      if (f === 0) continue
      const size = axis === 'x' ? el.clientWidth : el.clientHeight
      let delta: number
      if (reduced()) {
        if (now - g.lastStep < STEP_EVERY_MS) continue
        g.lastStep = now
        delta = Math.sign(f) * Math.max(1, Math.round(size * 0.8))
      } else {
        delta = Math.round(f * MAX_SPEED_PX) || Math.sign(f)
      }
      const before = axis === 'x' ? el.scrollLeft : el.scrollTop
      if (axis === 'x') el.scrollLeft = before + delta
      else el.scrollTop = before + delta
      if ((axis === 'x' ? el.scrollLeft : el.scrollTop) !== before) moved = true
    }
    return moved
  }

  function frame(now: number): void {
    if (g === null || !g.started) return
    place()
    if (autoScroll(now)) update()
    g.frame = requestAnimationFrame(frame)
  }

  function start(): void {
    if (g === null || g.started) return
    g.started = true
    g.timer = null
    const r = g.source.getBoundingClientRect()
    g.offsetX = g.startX - r.left
    g.offsetY = g.startY - r.top
    try {
      g.root.setPointerCapture(g.pointerId)
    } catch {
      // The pointer is gone already; the window listeners still end the drag.
    }
    const style = document.documentElement.style
    g.saved = { userSelect: style.userSelect, cursor: style.cursor }
    style.userSelect = 'none'
    style.cursor = 'grabbing'
    window.getSelection()?.removeAllRanges()
    ghost.value = { n: g.n, width: r.width }
    active.value = { n: g.n, over: null, index: 0, before: null, marker: null }
    update()
    if (typeof requestAnimationFrame === 'function') g.frame = requestAnimationFrame(frame)
    o.pickedUp?.(g.n)
  }

  function onMove(e: PointerEvent): void {
    if (g === null || e.pointerId !== g.pointerId) return
    g.x = e.clientX
    g.y = e.clientY
    const dist = Math.hypot(g.x - g.startX, g.y - g.startY)
    if (!g.started) {
      if (g.type === 'touch') {
        if (dist > TOUCH_SLOP_PX) end()
        return
      }
      if (dist < DRAG_THRESHOLD_PX) return
      start()
      return
    }
    e.preventDefault()
    update()
  }

  function onUp(e: PointerEvent): void {
    if (g === null || e.pointerId !== g.pointerId) return
    if (!g.started) {
      end()
      return
    }
    g.x = e.clientX
    g.y = e.clientY
    const at = locate()
    const n = g.n
    swallowClick()
    end()
    if (at === null || at.over === null) {
      o.cancelled?.(n, 'outside')
      return
    }
    const target = o.targets(n)[at.over]
    if (!target.allowed) {
      o.cancelled?.(n, 'refused', target)
      return
    }
    o.drop(n, target, at.before)
  }

  function onPointerCancel(e: PointerEvent): void {
    if (g === null || e.pointerId !== g.pointerId) return
    cancel('pointer')
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key !== 'Escape' || g === null) return
    e.preventDefault()
    e.stopPropagation()
    cancel('escape')
  }

  function onBlur(): void {
    cancel('blur')
  }

  function onHidden(): void {
    if (document.visibilityState === 'hidden') cancel('blur')
  }

  /** Stops the page from scrolling under a finger that drags a card. */
  function onTouchMove(e: TouchEvent): void {
    if (g?.started && e.cancelable) e.preventDefault()
  }

  function onPrevent(e: Event): void {
    if (g !== null && (g.started || g.type === 'touch')) e.preventDefault()
  }

  /** The release of a drag is not a click on whatever is under it. */
  function swallowClick(): void {
    const stop = (e: Event) => {
      e.preventDefault()
      e.stopPropagation()
    }
    window.addEventListener('click', stop, { capture: true, once: true })
    setTimeout(() => window.removeEventListener('click', stop, { capture: true }), 0)
  }

  function listen(on: boolean): void {
    const f = on ? window.addEventListener.bind(window) : window.removeEventListener.bind(window)
    f('pointermove', onMove as EventListener, { capture: true, passive: false } as AddEventListenerOptions)
    f('pointerup', onUp as EventListener, true)
    f('pointercancel', onPointerCancel as EventListener, true)
    f('keydown', onKey as EventListener, true)
    f('blur', onBlur)
    f('touchmove', onTouchMove as EventListener, { capture: true, passive: false } as AddEventListenerOptions)
    f('contextmenu', onPrevent, true)
    f('dragstart', onPrevent, true)
    f('selectstart', onPrevent, true)
    const d = on ? document.addEventListener.bind(document) : document.removeEventListener.bind(document)
    d('visibilitychange', onHidden)
  }

  function end(): void {
    const cur = g
    if (cur === null) return
    g = null
    if (cur.timer !== null) clearTimeout(cur.timer)
    if (cur.frame !== null) cancelAnimationFrame(cur.frame)
    listen(false)
    try {
      if (cur.root.hasPointerCapture(cur.pointerId)) cur.root.releasePointerCapture(cur.pointerId)
    } catch {
      // Already released.
    }
    if (cur.saved !== null) {
      const style = document.documentElement.style
      style.userSelect = cur.saved.userSelect
      style.cursor = cur.saved.cursor
    }
    active.value = null
    ghost.value = null
  }

  function cancel(why: CancelReason = 'escape'): void {
    if (g === null) return
    const { n, started } = g
    end()
    if (started) o.cancelled?.(n, why)
  }

  function onPointerDown(e: PointerEvent, n: number): void {
    if (g !== null || !e.isPrimary) return
    if (e.pointerType === 'mouse' && (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey)) return
    const from = e.target instanceof Element ? e.target : null
    if (from === null || from.closest(NO_DRAG) !== null) return
    const source = from.closest<HTMLElement>('li[data-ticket]')
    const root = e.currentTarget instanceof HTMLElement ? e.currentTarget : source
    if (source === null || root === null) return
    g = {
      pointerId: e.pointerId,
      type: e.pointerType,
      n,
      source,
      root,
      startX: e.clientX,
      startY: e.clientY,
      x: e.clientX,
      y: e.clientY,
      offsetX: 0,
      offsetY: 0,
      started: false,
      timer: null,
      frame: null,
      lastStep: 0,
      saved: null,
    }
    listen(true)
    if (e.pointerType === 'touch') g.timer = setTimeout(start, LONG_PRESS_MS)
  }

  return {
    active: readonly(active) as Readonly<Ref<DragActive | null>>,
    ghost: readonly(ghost) as Readonly<Ref<{ n: number; width: number } | null>>,
    setGhost,
    onPointerDown,
    cancel,
    refresh: update,
  }
}
