import { expect, test } from 'vitest'
import { summarizeWheelTrace, wheelTraceProblems } from '../../e2e/support/wheelTrace'

type Ev = { name: string; ph: string; ts?: number; pid?: number; tid?: number; args?: { dx?: number; dy?: number } }
const e = (name: string, args?: { dx?: number; dy?: number }): Ev => ({ name, ph: 'X', pid: 1, tid: 1, args })
const wheel = (dx: number, dy: number): Ev[] => [
  e('RenderWidgetHostImpl::ForwardWheelEvent', { dx, dy }),
  e('InputRouterImpl::MouseWheelEventHandled'),
  e('RenderWidgetHostImpl::ForwardWheelEvent', { dx: 0, dy: 0 }),
  e('InputRouterImpl::MouseWheelEventHandled'),
]

// What Chromium traced for a wheel over a box that its compositor scrolled by itself.
const compositorOnly: Ev[] = [
  ...wheel(-400, 0),
  e('InputHandlerProxy::HandleGestureScrollBegin'),
  e('InputHandlerProxy::HandleGestureScrollUpdate'),
  e('InputHandlerProxy::HandleGestureScrollEnd'),
]

// What it traced for a wheel that was lost: the begin needed the main thread's hit test and the update never reached the compositor.
const lost: Ev[] = [
  ...wheel(-400, 0),
  e('InputHandlerProxy::HandleGestureScrollBegin'),
  e('WidgetInputHandlerManager::FindScrollTargetOnMainThread'),
  e('WidgetInputHandlerManager::FindScrollTargetReply'),
  e('InputHandlerProxy::HandleGestureScrollBegin'),
  e('InputHandlerProxy::HandleGestureScrollEnd'),
]

const problems = (events: Ev[], expected = 1) => wheelTraceProblems(summarizeWheelTrace(events), expected).join('\n')

test('a wheel the compositor scrolled alone has no problems', () => {
  expect(wheelTraceProblems(summarizeWheelTrace(compositorOnly), 1)).toEqual([])
})

test('the sequence of a lost wheel is refused: a main-thread hit test and no compositor update', () => {
  const t = summarizeWheelTrace(lost)
  expect(t).toMatchObject({ wheels: 1, scrollBegins: 2, compositorUpdates: 0, mainThreadHitTests: 1 })
  expect(problems(lost)).toContain('main thread')
  expect(problems(lost)).toContain('0 scroll updates')
})

test('an empty trace cannot count as no main-thread hit tests', () => {
  expect(wheelTraceProblems(summarizeWheelTrace([]), 1)).toEqual(expect.arrayContaining(['the trace is empty', 'the trace shows 0 wheel events, expected 1']))
})

test('a trace cut short is refused wherever it stops: after the wheel, after the begin, after the update', () => {
  const [forwardedWheel, , , , begin, update, end] = [...compositorOnly]
  expect(problems([forwardedWheel!])).toContain('acknowledged 0 of 1')
  expect(problems([forwardedWheel!])).toContain('no scroll gesture begin')
  expect(problems([...wheel(-400, 0), begin!])).toContain('0 scroll updates')
  expect(problems([...wheel(-400, 0), begin!, update!])).toContain('ended 0 scroll gestures')
  expect(problems([...wheel(-400, 0), begin!, update!, end!])).toBe('')
})

test('spans that began and never ended are not counted and refuse the trace', () => {
  // One wheel, a begin and an update that were never completed: nothing here proves the compositor handled the wheel.
  const open: Ev[] = [
    e('RenderWidgetHostImpl::ForwardWheelEvent', { dy: 400 }),
    { name: 'InputHandlerProxy::HandleGestureScrollBegin', ph: 'B', pid: 1, tid: 2 },
    { name: 'InputHandlerProxy::HandleGestureScrollUpdate', ph: 'B', pid: 1, tid: 2 },
  ]
  const t = summarizeWheelTrace(open)
  expect(t).toMatchObject({ scrollBegins: 0, compositorUpdates: 0, unfinished: 2 })
  expect(problems(open)).toContain('2 trace spans never finished')
  // The same spans, ended, count.
  const ended: Ev[] = [...open, { name: 'InputHandlerProxy::HandleGestureScrollUpdate', ph: 'E', pid: 1, tid: 2 }, { name: 'InputHandlerProxy::HandleGestureScrollBegin', ph: 'E', pid: 1, tid: 2 }]
  expect(summarizeWheelTrace(ended)).toMatchObject({ scrollBegins: 1, compositorUpdates: 1, unfinished: 0 })
})

test('async spans are matched by their id and a wheel with no delta is not a wheel', () => {
  const b = { name: 'InputHandlerProxy::HandleGestureScrollUpdate', ph: 'b', pid: 1, tid: 1, cat: 'input', id: '0x1' }
  expect(summarizeWheelTrace([b]).unfinished).toBe(1)
  expect(summarizeWheelTrace([b, { ...b, ph: 'e' }]).unfinished).toBe(0)
  expect(summarizeWheelTrace([{ ...b, id: '0x2' }, { ...b, ph: 'e' }]).unfinished).toBe(1)
  expect(summarizeWheelTrace([...wheel(0, -400), ...wheel(-300, 0), ...wheel(0, 0)]).wheels).toBe(2)
})

test('complete spans remain complete when trace chunks arrive out of timestamp order', () => {
  const span = { name: 'InputHandlerProxy::HandleGestureScrollUpdate', cat: 'input', id: '0x1', pid: 1 }
  const begin = { ...span, ph: 'b', tid: 1, ts: 100 }
  const end = { ...span, ph: 'e', tid: 2, ts: 200 }
  expect(summarizeWheelTrace([end, ...compositorOnly, begin])).toEqual(summarizeWheelTrace([begin, ...compositorOnly, end]))
  // Ordering must not make a genuinely absent end look complete.
  expect(summarizeWheelTrace([...compositorOnly, begin]).unfinished).toBe(1)
})

test("the browser's input latency spans are bookkeeping: one still open when the trace stops does not make it incomplete", () => {
  const latency = { name: 'InputLatency::GestureScrollEnd', ph: 'b', pid: 1, tid: 1, cat: 'input', id: '0x9' }
  expect(summarizeWheelTrace([...compositorOnly, latency]).unfinished).toBe(0)
  expect(problems([...compositorOnly, latency])).toBe('')
})
