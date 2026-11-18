import { expect, test } from 'vitest'
import { summarizeWheelTrace, wheelTraceProblems } from '../../e2e/support/wheelTrace'

const e = (name: string, args?: { dx?: number; dy?: number }) => ({ name, ph: 'X', args })
const wheel = (dx: number, dy: number) => [e('RenderWidgetHostImpl::ForwardWheelEvent', { dx, dy }), e('RenderWidgetHostImpl::ForwardWheelEvent', { dx: 0, dy: 0 })]

// What Chromium traced for a wheel over a column that its compositor scrolled by itself.
const compositorOnly = [
  ...wheel(-400, 0),
  e('InputHandlerProxy::HandleGestureScrollBegin'),
  e('InputHandlerProxy::HandleGestureScrollUpdate'),
  e('InputHandlerProxy::HandleGestureScrollEnd'),
]

// What it traced for a wheel that was lost: the begin needed the main thread's hit test and the update never reached the compositor.
const lost = [
  ...wheel(-400, 0),
  e('InputHandlerProxy::HandleGestureScrollBegin'),
  e('WidgetInputHandlerManager::FindScrollTargetOnMainThread'),
  e('WidgetInputHandlerManager::FindScrollTargetReply'),
  e('InputHandlerProxy::HandleGestureScrollBegin'),
]

test('a wheel the compositor scrolled alone has no problems', () => {
  expect(wheelTraceProblems(summarizeWheelTrace(compositorOnly), 1)).toEqual([])
})

test('the sequence of a lost wheel is refused: a main-thread hit test and no compositor update', () => {
  const t = summarizeWheelTrace(lost)
  expect(t).toMatchObject({ wheels: 1, scrollBegins: 2, compositorUpdates: 0, mainThreadHitTests: 1 })
  const problems = wheelTraceProblems(t, 1).join('\n')
  expect(problems).toContain('main thread')
  expect(problems).toContain('0 scroll updates')
})

test('an empty, truncated or short trace cannot count as no main-thread hit tests', () => {
  expect(wheelTraceProblems(summarizeWheelTrace([]), 1)).toEqual(expect.arrayContaining(['the trace is empty', 'the trace shows 0 wheel events, expected 1']))
  expect(wheelTraceProblems(summarizeWheelTrace(wheel(-400, 0)), 1).join('\n')).toContain('no scroll gesture begin')
  expect(wheelTraceProblems(summarizeWheelTrace(compositorOnly), 2).join('\n')).toContain('expected 2')
})

test('end phases and zero-delta wheels are not wheels', () => {
  expect(summarizeWheelTrace([...wheel(0, -400), ...wheel(-300, 0), { name: 'RenderWidgetHostImpl::ForwardWheelEvent', ph: 'E', args: { dx: 5, dy: 5 } }]).wheels).toBe(2)
})
