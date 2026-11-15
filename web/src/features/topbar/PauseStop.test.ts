import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'
import App from '../../App.vue'
import { ApiError, type ApiClient, type ClientOptions } from '../../api/client'
import { createTokenStore } from '../../auth/token'
import { activeToasts, useToast } from '../../components/ui/useToast'
import { createAppRouter } from '../../router'
import { createSession, SESSION } from '../../session'
import { FakeOmo, MemoryStorage } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'

let mounted: VueWrapper | null = null
beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }))
afterEach(() => {
  vi.useRealTimers()
  mounted?.unmount()
  mounted = null
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

type Send = (method: string, path: string, body?: unknown) => Promise<unknown>

async function boot(send: Send, paused = false) {
  const omo = new FakeOmo()
  omo.paused = paused
  omo.tickets = [ticket({ number: 1 })]
  const makeClient = (o: ClientOptions): ApiClient => ({ ...omo.makeClient(o), send: (async (m: string, p: string, b?: unknown) => ({ data: await send(m, p, b), eventId: null })) as ApiClient['send'] })
  const router = createAppRouter({ history: createMemoryHistory() })
  await router.push('/')
  await router.isReady()
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  const wrapper = mount(App, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: session } } })
  mounted = wrapper
  await flushPromises()
  omo.stream!.onState('live')
  return { wrapper, omo }
}

const stopDialog = (w: VueWrapper) => w.findAll('dialog').find((d) => d.text().includes('Stop every running agent?'))!
const toggle = (w: VueWrapper) => w.get('button[role=switch]')
const checked = (w: VueWrapper) => toggle(w).attributes('aria-checked')

test('the switch shows the server state, asks to pause, and follows the server event', async () => {
  const calls: unknown[][] = []
  const { wrapper, omo } = await boot(async (...a) => {
    calls.push(a)
    return {}
  })
  expect(toggle(wrapper).text()).toBe('Paused')
  expect(checked(wrapper)).toBe('false')
  expect(wrapper.find('[data-paused-banner]').exists()).toBe(false)

  await toggle(wrapper).trigger('click')
  await flushPromises()
  expect(calls).toEqual([['PUT', '/api/server/paused', { paused: true }]])
  // Wanted state shown while pending; the banner waits for the server.
  expect(checked(wrapper)).toBe('true')
  expect(wrapper.find('[data-paused-banner]').exists()).toBe(false)

  omo.emit('server.paused', { paused: true })
  await flushPromises()
  expect(checked(wrapper)).toBe('true')
  expect(wrapper.get('[data-paused-banner]').text()).toBe('Paused: omo starts no new agents.')
})

test('a refusal switches back at once and shows the server message', async () => {
  const { wrapper } = await boot(async () => {
    throw new ApiError(500, 'internal', 'The database is locked.')
  })
  await toggle(wrapper).trigger('click')
  await flushPromises()
  expect(checked(wrapper)).toBe('false')
  expect(activeToasts.map((t) => t.message)).toContain('The database is locked.')
})

test('without the server confirming, the switch shows the server state again after the pending period', async () => {
  const { wrapper } = await boot(async () => ({}))
  await toggle(wrapper).trigger('click')
  await flushPromises()
  expect(checked(wrapper)).toBe('true')
  vi.advanceTimersByTime(5000)
  await flushPromises()
  expect(checked(wrapper)).toBe('false')
})

test('stop all asks first, says what it does, and shows a refusal in the server words', async () => {
  const calls: unknown[][] = []
  const { wrapper } = await boot(async (...a) => {
    calls.push(a)
    throw new ApiError(503, 'unavailable', 'agents are not available in this server')
  })
  await wrapper.get('button[aria-label="Stop all agents"]').trigger('click')
  await flushPromises()
  expect(calls).toEqual([])
  const dialog = stopDialog(wrapper)
  expect(dialog.text()).toContain('Stop every running agent?')
  expect(dialog.text()).toContain('The CEO keeps running')
  expect(dialog.text()).toContain('pauses')

  const confirm = dialog.findAll('button').find((b) => b.text() === 'Stop all agents')!
  await confirm.trigger('click')
  await flushPromises()
  expect(calls).toEqual([['POST', '/api/agents/stop-all', {}]])
  expect(dialog.get('[data-stop-error]').text()).toBe('agents are not available in this server')
  expect(activeToasts.some((t) => t.tone === 'error')).toBe(true)
})

test('stop all closes the dialog and confirms when the server did it', async () => {
  const { wrapper } = await boot(async () => ({}))
  await wrapper.get('button[aria-label="Stop all agents"]').trigger('click')
  await flushPromises()
  await stopDialog(wrapper).findAll('button').find((b) => b.text() === 'Stop all agents')!.trigger('click')
  await flushPromises()
  expect((stopDialog(wrapper).element as HTMLDialogElement).open).toBe(false)
  expect(activeToasts.map((t) => t.title)).toContain('All agents stopped')
})
