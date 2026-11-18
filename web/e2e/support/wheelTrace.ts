import type { Page } from '@playwright/test'

/**
 * What Chromium's input pipeline did with the wheels sent while a page was
 * traced. A wheel is first sent as a gesture to the compositor, which scrolls
 * the box under the pointer itself. When that box needs a hit test on the main
 * thread (a nested scroller the compositor does not own), the compositor asks
 * the main thread for the target, and under load the wheel's scroll update is
 * sometimes dropped: no scroll happens at all, with nothing to wait for. A
 * test that turns the wheel therefore also checks, from the trace, that the
 * wheel never took that path.
 */
export interface WheelTrace {
  /** Wheel events with a delta that the browser process forwarded to the page. */
  wheels: number
  /** Every wheel event forwarded, the zero-delta ones that end a sequence included. */
  forwarded: number
  /** Forwarded wheel events the page's input router acknowledged. */
  acknowledged: number
  /** Scroll gestures the compositor began (a main-thread hit test makes it begin twice). */
  scrollBegins: number
  /** Scroll updates the compositor applied itself. */
  compositorUpdates: number
  /** Scroll gestures the compositor ended. */
  scrollEnds: number
  /** Wheel hit tests the compositor had to ask the main thread for. */
  mainThreadHitTests: number
  /** Spans that began and never ended before the trace stopped (a trace cut short). */
  unfinished: number
  /** Trace events in all: an empty trace proves nothing. */
  events: number
}

interface TraceEvent {
  name?: string
  ph?: string
  pid?: number
  tid?: number
  cat?: string
  id?: string | number
  id2?: unknown
  args?: { dx?: number; dy?: number }
}

/**
 * The events that are complete: a `X` event is one by itself, a `B` or async
 * `b` event only when its `E` or `e` is in the trace too. A span still open
 * when the trace stopped is not counted and is reported as unfinished.
 */
function completed(events: readonly TraceEvent[]): { done: TraceEvent[]; unfinished: number } {
  const done: TraceEvent[] = []
  const open = new Map<string, TraceEvent[]>()
  const key = (e: TraceEvent) => (e.ph === 'B' || e.ph === 'E' ? `${e.pid}/${e.tid}` : `${e.cat}/${e.name}/${e.id ?? JSON.stringify(e.id2)}`)
  for (const e of events) {
    if (e.ph === 'X') done.push(e)
    else if (e.ph === 'B' || e.ph === 'b') open.set(key(e), [...(open.get(key(e)) ?? []), e])
    else if (e.ph === 'E' || e.ph === 'e') {
      const started = open.get(key(e))?.pop()
      if (started) done.push(started)
    }
  }
  let unfinished = 0
  for (const left of open.values()) unfinished += left.length
  return { done, unfinished }
}

/** Counts what matters in the events of a Chromium trace taken with the `input` category. */
export function summarizeWheelTrace(events: readonly TraceEvent[]): WheelTrace {
  const { done, unfinished } = completed(events)
  const count = (name: string, keep: (e: TraceEvent) => boolean = () => true) => done.filter((e) => e.name === name && keep(e)).length
  return {
    wheels: count('RenderWidgetHostImpl::ForwardWheelEvent', (e) => (e.args?.dx ?? 0) !== 0 || (e.args?.dy ?? 0) !== 0),
    forwarded: count('RenderWidgetHostImpl::ForwardWheelEvent'),
    acknowledged: count('InputRouterImpl::MouseWheelEventHandled'),
    scrollBegins: count('InputHandlerProxy::HandleGestureScrollBegin'),
    compositorUpdates: count('InputHandlerProxy::HandleGestureScrollUpdate'),
    scrollEnds: count('InputHandlerProxy::HandleGestureScrollEnd'),
    mainThreadHitTests: count('WidgetInputHandlerManager::FindScrollTargetOnMainThread'),
    unfinished,
    events: events.length,
  }
}

/**
 * Why a trace does not show `expected` wheels scrolled by the compositor alone;
 * an empty list when it does. The trace must show the whole lifecycle first:
 * every wheel forwarded and acknowledged, a scroll begun, every wheel applied
 * as a compositor update and the gesture ended, with no span left open. A trace
 * cut short fails here instead of counting as "no main-thread hit tests".
 */
export function wheelTraceProblems(t: WheelTrace, expected: number): string[] {
  const problems: string[] = []
  if (t.events === 0) problems.push('the trace is empty')
  if (t.unfinished > 0) problems.push(`${t.unfinished} trace spans never finished: the trace was cut short`)
  if (t.wheels !== expected) problems.push(`the trace shows ${t.wheels} wheel events, expected ${expected}`)
  if (t.acknowledged < t.forwarded) problems.push(`the page acknowledged ${t.acknowledged} of ${t.forwarded} wheel events`)
  if (t.scrollBegins < 1) problems.push('the trace shows no scroll gesture begin')
  if (t.compositorUpdates < expected) problems.push(`the compositor applied ${t.compositorUpdates} scroll updates for ${expected} wheels`)
  if (t.scrollEnds < expected) problems.push(`the compositor ended ${t.scrollEnds} scroll gestures for ${expected} wheels`)
  if (t.mainThreadHitTests > 0) problems.push(`${t.mainThreadHitTests} wheel hit tests went to the main thread, where a wheel can be dropped`)
  return problems
}

/**
 * Runs `turn` (which sends trusted wheels and waits for their effect) with the
 * browser's input trace on and returns what the pipeline did. The trace only
 * records; it adds no listener to the page.
 */
export async function traceWheels(page: Page, turn: () => Promise<void>): Promise<WheelTrace> {
  const browser = page.context().browser()
  if (!browser) throw new Error('a wheel trace needs the browser the page runs in')
  await browser.startTracing(page, { categories: ['input'] })
  let traced: TraceEvent[] = []
  try {
    await turn()
    // The input pipeline hands a wheel on to the page after acknowledging it: a frame of the page after the last wheel has seen the whole gesture.
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
  } finally {
    const buffer = await browser.stopTracing()
    traced = (JSON.parse(buffer.toString()) as { traceEvents: TraceEvent[] }).traceEvents
  }
  return summarizeWheelTrace(traced)
}
