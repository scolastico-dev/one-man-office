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
  /** Scroll gestures the compositor began (a main-thread hit test makes it begin twice). */
  scrollBegins: number
  /** Scroll updates the compositor applied itself. */
  compositorUpdates: number
  /** Wheel hit tests the compositor had to ask the main thread for. */
  mainThreadHitTests: number
  /** Trace events in all: an empty or truncated trace proves nothing. */
  events: number
}

interface TraceEvent {
  name?: string
  ph?: string
  args?: { dx?: number; dy?: number }
}

/** Counts what matters in the events of a Chromium trace taken with the `input` category. */
export function summarizeWheelTrace(events: readonly TraceEvent[]): WheelTrace {
  const count = (name: string, keep: (e: TraceEvent) => boolean = () => true) =>
    events.filter((e) => e.name === name && e.ph !== 'E' && e.ph !== 'e' && keep(e)).length
  return {
    wheels: count('RenderWidgetHostImpl::ForwardWheelEvent', (e) => (e.args?.dx ?? 0) !== 0 || (e.args?.dy ?? 0) !== 0),
    scrollBegins: count('InputHandlerProxy::HandleGestureScrollBegin'),
    compositorUpdates: count('InputHandlerProxy::HandleGestureScrollUpdate'),
    mainThreadHitTests: count('WidgetInputHandlerManager::FindScrollTargetOnMainThread'),
    events: events.length,
  }
}

/**
 * Why a trace does not show `expected` wheels scrolled by the compositor alone;
 * an empty list when it does. The trace must be complete first: every wheel
 * seen, a scroll begun, and every wheel applied as a compositor update.
 */
export function wheelTraceProblems(t: WheelTrace, expected: number): string[] {
  const problems: string[] = []
  if (t.events === 0) problems.push('the trace is empty')
  if (t.wheels !== expected) problems.push(`the trace shows ${t.wheels} wheel events, expected ${expected}`)
  if (t.scrollBegins < 1) problems.push('the trace shows no scroll gesture begin')
  if (t.compositorUpdates < expected) problems.push(`the compositor applied ${t.compositorUpdates} scroll updates for ${expected} wheels`)
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
  } finally {
    const buffer = await browser.stopTracing()
    traced = (JSON.parse(buffer.toString()) as { traceEvents: TraceEvent[] }).traceEvents
  }
  return summarizeWheelTrace(traced)
}
