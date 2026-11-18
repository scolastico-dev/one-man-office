import type { BrowserContext, Page, Route } from '@playwright/test'
import { expect, test, vi } from 'vitest'
import { cpuThrottleRate, installCPUThrottle } from '../../e2e/support/throttle'

test('throttling is opt-in and invalid rates fail clearly', () => {
  expect(cpuThrottleRate(undefined)).toBe(1)
  expect(cpuThrottleRate('4')).toBe(4)
  for (const value of ['', ' ', '0', '-1', 'NaN', 'Infinity', 'fast']) expect(() => cpuThrottleRate(value)).toThrow('E2E_CPU_THROTTLE_RATE')
})

function fixture() {
  const page = { isClosed: () => false } as Page
  const popup = { isClosed: () => false } as Page
  const send = vi.fn(async () => {})
  const detach = vi.fn(async () => {})
  const context = {
    newPage: vi.fn(async () => page),
    pages: () => [],
    newCDPSession: vi.fn(async () => ({ send, detach })),
    route: vi.fn(async () => {}),
    unroute: vi.fn(async () => {}),
    on: vi.fn(),
    off: vi.fn(),
  }
  return { page, popup, send, detach, context: context as unknown as BrowserContext }
}

test('default mode leaves page creation and networking untouched', async () => {
  const { context } = fixture()
  const original = context.newPage
  const dispose = await installCPUThrottle(context, 1)
  expect(context.newPage).toBe(original)
  expect(context.route).not.toHaveBeenCalled()
  await dispose()
})

test('the requested CDP rate is acknowledged before a page is returned or popup navigation proceeds', async () => {
  const { context, page, popup, send, detach } = fixture()
  const original = context.newPage
  let acknowledge!: () => void
  const gate = new Promise<void>((r) => { acknowledge = r })
  send.mockImplementationOnce(() => gate)
  const dispose = await installCPUThrottle(context, 4)
  try {
    let returned = false
    const opening = context.newPage().then((p) => { returned = true; return p })
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith('Emulation.setCPUThrottlingRate', { rate: 4 }))
    expect(returned).toBe(false)
    acknowledge()
    expect(await opening).toBe(page)
    const handler = vi.mocked(context.route).mock.calls[0]![1] as (route: Route) => Promise<void>
    const fallback = vi.fn(async () => {})
    const request = { request: () => ({ isNavigationRequest: () => true, frame: () => ({ page: () => popup }) }), fallback } as unknown as Route
    await handler(request)
    expect(context.newCDPSession).toHaveBeenCalledWith(popup)
    expect(send).toHaveBeenCalledTimes(2)
    expect(fallback).toHaveBeenCalledOnce()
    // More resources or navigations in the same page do not make extra sessions.
    await handler(request)
    expect(send).toHaveBeenCalledTimes(2)
  } finally {
    acknowledge()
    await dispose()
  }
  expect(context.newPage).toBe(original)
  expect(detach).toHaveBeenCalledTimes(2)
})


test('a popup document may be requested before its frame exists; its resources wait for the rate', async () => {
  const { context, popup, send } = fixture()
  const dispose = await installCPUThrottle(context, 4)
  try {
    const handler = vi.mocked(context.route).mock.calls[0]![1] as (route: Route) => Promise<void>
    const fallback = vi.fn(async () => {})
    await handler({ request: () => ({ isNavigationRequest: () => true, frame: () => { throw new Error('Frame for this navigation request is not available, because the request was issued before the frame is created.') } }), fallback } as unknown as Route)
    expect(fallback).toHaveBeenCalledOnce()
    let acknowledge!: () => void
    const gate = new Promise<void>((r) => { acknowledge = r })
    send.mockImplementationOnce(() => gate)
    const resourceFallback = vi.fn(async () => {})
    const resource = handler({ request: () => ({ isNavigationRequest: () => false, frame: () => ({ page: () => popup }) }), fallback: resourceFallback } as unknown as Route)
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith('Emulation.setCPUThrottlingRate', { rate: 4 }))
    expect(resourceFallback).not.toHaveBeenCalled()
    acknowledge()
    await resource
    expect(resourceFallback).toHaveBeenCalledOnce()
  } finally {
    await dispose()
  }
})


test('cleanup accepts an already closed popup but propagates unrelated CDP errors', async () => {
  const { context, page, detach } = fixture()
  const dispose = await installCPUThrottle(context, 4)
  await context.newPage()
  Object.assign(page, { isClosed: () => true })
  detach.mockRejectedValueOnce(new Error('Target page, context or browser has been closed'))
  await expect(dispose()).resolves.toBeUndefined()

  const other = fixture()
  const disposeOther = await installCPUThrottle(other.context, 4)
  await other.context.newPage()
  other.detach.mockRejectedValueOnce(new Error('CDP failed'))
  await expect(disposeOther()).rejects.toThrow('CDP failed')
})

test('a failed throttle command is reported after its CDP session is cleaned up', async () => {
  const { context, send, detach } = fixture()
  send.mockRejectedValueOnce(new Error('rate command failed'))
  const dispose = await installCPUThrottle(context, 4)
  await expect(context.newPage()).rejects.toThrow('rate command failed')
  await expect(dispose()).rejects.toThrow('rate command failed')
  expect(detach).toHaveBeenCalledOnce()
})


test.each(['attach', 'command'])('a popup that closes during %s does not fail teardown', async (phase) => {
  const { context, page, send, detach } = fixture()
  let reject!: (e: Error) => void
  const gate = new Promise<never>((_, r) => { reject = r })
  if (phase === 'attach') vi.mocked(context.newCDPSession).mockImplementationOnce(() => gate)
  else send.mockImplementationOnce(() => gate)
  const dispose = await installCPUThrottle(context, 4)
  const opening = context.newPage()
  await vi.waitFor(() => expect(phase === 'attach' ? context.newCDPSession : send).toHaveBeenCalled())
  Object.assign(page, { isClosed: () => true })
  reject(new Error('Target page, context or browser has been closed'))
  await expect(opening).resolves.toBe(page)
  await expect(dispose()).resolves.toBeUndefined()
  expect(detach).toHaveBeenCalledTimes(phase === 'attach' ? 0 : 1)
})
