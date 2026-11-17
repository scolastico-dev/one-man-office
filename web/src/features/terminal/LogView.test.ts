import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ApiError } from '../../api/client'
import { createTokenStore } from '../../auth/token'
import { createSession, SESSION } from '../../session'
import { FakeOmo, MemoryStorage } from '../../testing/fakeOmo'
import LogView from './LogView.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

type Answer = { text: string; headers: Headers } | ApiError

async function view(answer: (path: string) => Answer, sessionId = 's1') {
  const omo = new FakeOmo()
  const paths: string[] = []
  const session = createSession({
    tokens: createTokenStore(() => new MemoryStorage()),
    makeClient: (o) => ({
      ...omo.makeClient(o),
      getText: async (path: string) => {
        paths.push(path)
        const a = answer(path)
        if (a instanceof ApiError) throw a
        return a
      },
    }),
    connect: omo.connect,
  })
  const w = mount(LogView, { props: { sessionId }, attachTo: document.body, global: { provide: { [SESSION as symbol]: session } } })
  mounted = w
  await flushPromises()
  return { w, paths }
}

const ok = (text: string, truncated = false): Answer => ({ text, headers: new Headers(truncated ? { 'Omo-Log-Truncated': 'true' } : {}) })

test('log view: text only, keeps lines, writes control and bidi characters out', async () => {
  const { w, paths } = await view(() => ok('first <b>line</b>\r\n\tsecond \u001b[31m‮evil\nthird <script>x</script>'))
  expect(paths).toEqual(['/api/sessions/s1/log'])
  const pre = w.get('pre')
  expect(pre.element.querySelector('b, script')).toBeNull()
  expect(pre.text()).toBe('first <b>line</b>\n    second \\u{1B}[31m\\u{202E}evil\nthird <script>x</script>')
  expect(w.find('[data-log-truncated]').exists()).toBe(false)
})

test('log view: says when only the last 2 MiB are shown', async () => {
  const { w } = await view(() => ok('tail', true))
  expect(w.get('[data-log-truncated]').text()).toBe('Showing the last 2 MiB.')
  expect(w.get('pre').text()).toBe('tail')
})

test('log view: the missing-log message is shown as the server wrote it', async () => {
  const msg = 'The log of this session is no longer kept.'
  const { w } = await view(() => new ApiError(404, 'not_found', msg))
  expect(w.get('[data-log-error]').text()).toBe(msg)
  expect(w.find('pre').exists()).toBe(false)
})

test('log view: an empty log says so', async () => {
  const { w } = await view(() => ok(''))
  expect(w.find('[data-log-empty]').exists()).toBe(true)
})
