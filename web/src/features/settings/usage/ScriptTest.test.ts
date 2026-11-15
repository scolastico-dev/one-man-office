import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { ApiError } from '../../../api/client'
import type { ProfilesResponse, UsageTestResult } from '../../../api/types'
import { announcements } from '../../../components/ui/useAnnouncer'
import ScriptTest from './ScriptTest.vue'
import { mountIn, setup } from './usageTesting'

const profiles = (...names: string[]): ProfilesResponse =>
  ({ profiles: names.map((name) => ({ name, cli: 'custom', command: 'x', args: [], env: {}, account_dir: '', usage_script: '', prompt_delivery: {}, models: {} })), errors: [] }) as ProfilesResponse

const usage = { soft_percent: 90, hard_percent: 98, refresh_interval: '5m', windows: { b: { soft_percent: 50 } } }

async function mounted(answer: () => unknown) {
  const s = await setup()
  s.state.get['/api/profiles'] = () => profiles('other', 'fake')
  s.state.write = answer
  const w = await mountIn(s, ScriptTest, { name: 'fake-usage', draft: 'function usage() end', usedBy: ['fake'], usage })
  return { s, w }
}

const run = async (w: VueWrapper) => {
  await w.get('[data-test-run]').trigger('click')
  await flushPromises()
}

test('test: shows windows as meters and the error as text', async () => {
  const result: UsageTestResult = {
    windows: [
      { name: 'a', used_percent: 42, resets_at: null },
      { name: 'b', used_percent: 60, resets_at: null },
    ],
    error: '',
    log: ['read <b>file</b>', 'token [redacted]‮'],
    millis: 812,
  }
  const { s, w } = await mounted(() => result)
  // The profiles that use the script come first.
  const select = w.get<HTMLSelectElement>('select')
  expect([...select.element.options].map((o) => o.text)).toEqual(['fake (uses this script)', 'other'])
  await run(w)

  expect(s.sent).toEqual([{ method: 'POST', path: '/api/usage/scripts/fake-usage/test', body: { text: 'function usage() end', profile: 'fake' }, ifMatch: undefined }])
  expect(w.get('[data-test-result]').text()).toContain('Ran as fake · took 812 ms')
  // The result is said once, in one sentence; the meters and the log are not read out again.
  expect(announcements.polite.value).toBe('The script reported 2 windows.')
  expect(['status', 'alert']).not.toContain(w.get('[data-test-result]').attributes('role'))
  expect(w.get('[data-test-result]').attributes('aria-live')).toBeUndefined()
  const meters = w.findAll('[role="meter"]')
  expect(meters.map((m) => m.attributes('aria-valuenow'))).toEqual(['42', '60'])
  // b has its own soft limit: 60 % is past it.
  expect(w.get('[data-test-window="b"]').text()).toContain('near its limit')
  expect(w.get('[data-test-window="a"]').text()).not.toContain('near its limit')
  const log = w.get('[data-test-log]')
  expect(log.find('b').exists()).toBe(false)
  expect(log.text()).toContain('read <b>file</b>')
  expect(log.text()).not.toContain('‮')

  // A failing run: the error in a Problem callout, as text.
  s.state.write = () => ({ windows: [], error: 'line 3: no <i>file</i>‮', log: [], millis: 4 })
  await run(w)
  const error = w.get('[data-test-error]')
  expect(error.text()).toContain('The script failed')
  expect(error.text()).toContain('line 3: no <i>file</i>')
  expect(error.find('i').exists()).toBe(false)
  expect(error.text()).not.toContain('‮')
  expect(w.findAll('[role="meter"]')).toHaveLength(0)
})

test('test: a test already running and a refused draft are shown verbatim in place', async () => {
  const { s, w } = await mounted(() => {
    throw new ApiError(409, 'conflict', 'A usage script test is already running. Try again when it has finished.')
  })
  await run(w)
  expect(w.get('[data-test-refused]').text()).toContain('A usage script test is already running. Try again when it has finished.')
  expect(w.get('[data-test-refused]').attributes('role')).toBe('note')
  expect(announcements.assertive.value).toBe('A usage script test is already running. Try again when it has finished.')

  s.state.write = () => {
    throw new ApiError(422, 'validation', 'there is no profile of that name', { field: 'profile' })
  }
  await run(w)
  expect(w.find('[data-test-refused]').exists()).toBe(false)
  expect(w.get('select').attributes('aria-invalid')).toBe('true')
  expect(w.get('select').element.closest('.omo-field')!.textContent).toContain('there is no profile of that name')

  s.state.write = () => {
    throw new ApiError(422, 'validation', 'a usage script must be at most 64 KiB', { field: 'text' })
  }
  await run(w)
  expect(w.get('[data-test-refused]').text()).toContain('a usage script must be at most 64 KiB')
})

test('test: meters follow the server rules: a failed run is unknown, a window past its reset counts 0 %', async () => {
  const past = new Date(Date.now() - 60_000).toISOString()
  const { s, w } = await mounted(() => ({ windows: [{ name: 'a', used_percent: 99, resets_at: past }], error: '', log: [], millis: 3 }))
  await run(w)
  const a = w.get('[data-test-window="a"]')
  expect(a.get('[data-state]').attributes('data-state')).toBe('ok')
  expect(a.get('[data-usage-value]').text()).toBe('0 %')

  s.state.write = () => ({ windows: [{ name: 'a', used_percent: 99, resets_at: null }], error: 'line 9: half done', log: [], millis: 3 })
  await run(w)
  const failed = w.get('[data-test-window="a"]')
  expect(failed.get('[data-state]').attributes('data-state')).toBe('unknown')
  expect(failed.get('[data-usage-value]').text()).toBe('?')
})
