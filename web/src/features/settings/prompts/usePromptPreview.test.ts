import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { PromptPreview } from '../../../api/types'
import { createPromptPreview } from './usePromptPreview'

interface Held {
  name: string
  text: string
  resolve(v: PromptPreview): void
  reject(e: unknown): void
}

function setup(delay = 300) {
  const asked: Held[] = []
  const p = createPromptPreview({
    delay,
    request: (name, text) =>
      new Promise<PromptPreview>((resolve, reject) => {
        asked.push({ name, text, resolve, reject })
      }),
  })
  return { p, asked }
}

const tick = () => Promise.resolve().then(() => Promise.resolve())

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

test('requests are debounced: typing sends one request, for the last text', () => {
  const { p, asked } = setup()
  p.schedule('stalled', 'a')
  vi.advanceTimersByTime(200)
  p.schedule('stalled', 'ab')
  vi.advanceTimersByTime(200)
  p.schedule('stalled', 'abc')
  expect(asked).toHaveLength(0)
  expect(p.busy.value).toBe(true)
  vi.advanceTimersByTime(300)
  expect(asked.map((a) => a.text)).toEqual(['abc'])
})

test('an older answer that arrives after a newer one never replaces it', async () => {
  const { p, asked } = setup()
  p.now('stalled', 'first')
  p.now('stalled', 'second')
  expect(asked.map((a) => a.text)).toEqual(['first', 'second'])
  asked[1]!.resolve({ text: 'SECOND', error: '' })
  await tick()
  expect(p.text.value).toBe('SECOND')
  expect(p.busy.value).toBe(false)
  asked[0]!.resolve({ text: 'FIRST', error: '' })
  await tick()
  expect(p.text.value).toBe('SECOND')
})

test('an older answer that arrives before the newer one is not shown either', async () => {
  const { p, asked } = setup()
  p.now('stalled', 'first')
  p.schedule('stalled', 'second')
  asked[0]!.resolve({ text: 'FIRST', error: '' })
  await tick()
  expect(p.text.value).toBe('')
  expect(p.ready.value).toBe(false)
  expect(p.busy.value).toBe(true)
  vi.advanceTimersByTime(300)
  asked[1]!.resolve({ text: 'SECOND', error: '' })
  await tick()
  expect(p.text.value).toBe('SECOND')
  expect(p.busy.value).toBe(false)
})

test('a template error is shown as the server says it, with no text', async () => {
  const { p, asked } = setup()
  p.now('stalled', '{{nme}}')
  asked[0]!.resolve({ text: '', error: 'template: stalled:1: function "nme" not defined' })
  await tick()
  expect(p.error.value).toBe('template: stalled:1: function "nme" not defined')
  expect(p.text.value).toBe('')
  p.now('stalled', 'fine')
  asked[1]!.resolve({ text: 'fine', error: '' })
  await tick()
  expect(p.error.value).toBe('')
  expect(p.text.value).toBe('fine')
})

test('a failed request shows its message, an old failure is ignored, stop drops what is under way', async () => {
  const { p, asked } = setup()
  p.now('x', 'one')
  p.now('x', 'two')
  asked[0]!.reject(new Error('old failure'))
  await tick()
  expect(p.failure.value).toBe('')
  asked[1]!.reject(new Error('The server is not reachable.'))
  await tick()
  expect(p.failure.value).toBe('The server is not reachable.')

  p.now('x', 'three')
  p.stop()
  asked[2]!.resolve({ text: 'THREE', error: '' })
  await tick()
  expect(p.text.value).toBe('')
  expect(p.busy.value).toBe(false)
})
