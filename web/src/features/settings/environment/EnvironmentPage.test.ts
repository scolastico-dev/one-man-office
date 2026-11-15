import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import { HIDDEN_VALUE, type General, type SettingsResponse } from '../../../api/types'
import EnvironmentPage from './EnvironmentPage.vue'
import { SHIPPED_ENV } from './envForm'

let w: VueWrapper | null = null
const saved = { ...general }
beforeEach(() => {
  general.agent_env = { PAGER: 'cat', OPENAI_API_KEY: HIDDEN_VALUE, TOKEN_CMD: '`echo ok`' }
})
afterEach(() => {
  w?.unmount()
  w = null
  Object.assign(general, saved)
  vi.restoreAllMocks()
})

async function open(h: Harness) {
  w = await mountWith(h, EnvironmentPage, {})
  await flushPromises()
  return w
}

const rows = (wr: VueWrapper) => wr.findAll('[data-env-row]').map((r) => r.attributes('data-env-row'))
const row = (wr: VueWrapper, name: string) => wr.get(`[data-env-row="${name}"]`)

test('Check sends the draft with hidden values as placeholders and shows states only', async () => {
  const h = await harness()
  h.respond.write = (s) => {
    expect(s.method).toBe('POST')
    expect(s.path).toBe('/api/settings/agent-env/check')
    return ok({
      variables: [
        { name: 'OPENAI_API_KEY', state: 'set', message: '' },
        { name: 'PAGER', state: 'set', message: '' },
        { name: 'TOKEN_CMD', state: 'failed', message: 'its command did not finish within 10s' },
      ],
    })
  }
  const wr = await open(h)
  expect(rows(wr)).toEqual(['OPENAI_API_KEY', 'PAGER', 'TOKEN_CMD'])
  expect(wr.text()).not.toContain('omo:hidden')
  await wr.get('[data-env-check-run]').trigger('click')
  await flushPromises()
  expect(h.sent).toHaveLength(1)
  expect(h.sent[0]!.body).toEqual({ env: { PAGER: 'cat', OPENAI_API_KEY: HIDDEN_VALUE, TOKEN_CMD: '`echo ok`' } })
  const result = wr.get('[data-env-check-result]')
  expect(result.text()).toContain('1 of 3 variables failed')
  expect(result.get('[data-check="PAGER"]').text()).toBe('PAGER: set')
  expect(result.get('[data-check="TOKEN_CMD"]').text()).toBe('TOKEN_CMD: failed, its command did not finish within 10s')
  expect(row(wr, 'TOKEN_CMD').text()).toContain('its command did not finish within 10s')
  expect(wr.text()).not.toContain('omo:hidden')
})

test('a result is cleared when its row is edited', async () => {
  const h = await harness()
  h.respond.write = () => ok({ variables: [{ name: 'PAGER', state: 'set', message: '' }, { name: 'TOKEN_CMD', state: 'set', message: '' }] })
  const wr = await open(h)
  await wr.get('[data-env-check-run]').trigger('click')
  await flushPromises()
  expect(wr.findAll('[data-check]')).toHaveLength(2)
  await row(wr, 'PAGER').get('[data-part="value"]').setValue('less')
  await flushPromises()
  expect(wr.findAll('[data-check]').map((c) => c.attributes('data-check'))).toEqual(['TOKEN_CMD'])
})

test('a refused check names the row; a running check shows the server message', async () => {
  const h = await harness()
  let n = 0
  h.respond.write = () => (n++ === 0 ? refuse('validation', 'Enter the value of OPENAI_API_KEY again.', { field: 'agent_env.OPENAI_API_KEY' }) : refuse('conflict', 'An environment check is already running. Try again when it has finished.', undefined, 409))
  const wr = await open(h)
  await wr.get('[data-env-check-run]').trigger('click')
  await flushPromises()
  expect(row(wr, 'OPENAI_API_KEY').text()).toContain('Enter the value of OPENAI_API_KEY again.')
  await wr.get('[data-env-check-run]').trigger('click')
  await flushPromises()
  expect(wr.get('[data-env-check-error]').text()).toContain('already running')
  expect(row(wr, 'OPENAI_API_KEY').text()).not.toContain('Enter the value')
})

test('restore asks and only changes the draft', async () => {
  const h = await harness()
  window.confirm = vi.fn().mockReturnValueOnce(false).mockReturnValueOnce(true)
  const wr = await open(h)
  await wr.get('[data-restore-defaults]').trigger('click')
  await flushPromises()
  expect(rows(wr)).toEqual(['OPENAI_API_KEY', 'PAGER', 'TOKEN_CMD'])
  await wr.get('[data-restore-defaults]').trigger('click')
  await flushPromises()
  expect(rows(wr)).toEqual(Object.keys(SHIPPED_ENV).sort((a, b) => a.localeCompare(b)))
  expect(h.sent).toHaveLength(0)
})

test('Save sends only agent_env with the revision and shows the stored answer', async () => {
  const h = await harness()
  h.respond.write = (s) => {
    expect(s.method).toBe('PATCH')
    expect(s.path).toBe('/api/settings')
    expect(s.body).toEqual({ agent_env: { PAGER: 'less', OPENAI_API_KEY: HIDDEN_VALUE, TOKEN_CMD: '`echo ok`' } })
    expect(s.ifMatch).toBe('0')
    const settings: General = { ...general, agent_env: (s.body as { agent_env: Record<string, string> }).agent_env }
    return ok({ settings, errors: [], revision: 'abc', home: '/h' } satisfies SettingsResponse)
  }
  const wr = await open(h)
  await row(wr, 'PAGER').get('[data-part="value"]').setValue('less')
  await wr.get('[data-primary]').trigger('click')
  await flushPromises()
  expect(h.sent).toHaveLength(1)
})

test('results are cleared when a save goes through and when a read brings other stored values', async () => {
  const h = await harness()
  const check = () => ok({ variables: [{ name: 'PAGER', state: 'set', message: '' }, { name: 'TOKEN_CMD', state: 'set', message: '' }] })
  h.respond.write = (s) => {
    if (s.path === '/api/settings/agent-env/check') return check()
    const settings: General = { ...general, agent_env: (s.body as { agent_env: Record<string, string> }).agent_env }
    return ok({ settings, errors: [], revision: 'abc', home: '/h' } satisfies SettingsResponse)
  }
  const wr = await open(h)
  await wr.get('[data-env-check-run]').trigger('click')
  await flushPromises()
  expect(wr.findAll('[data-check]')).toHaveLength(2)
  // Saved with another row changed: what was checked is no longer what agents get.
  await row(wr, 'OPENAI_API_KEY').get('[data-part="remove"]').trigger('click')
  await wr.get('[data-primary]').trigger('click')
  await flushPromises()
  expect(h.sent.at(-1)!.method).toBe('PATCH')
  expect(wr.find('[data-env-check-result]').exists()).toBe(false)

  await wr.get('[data-env-check-run]').trigger('click')
  await flushPromises()
  expect(wr.findAll('[data-check]')).toHaveLength(2)
  // Another tab changes the stored variables; the clean form shows them and the old results go.
  general.agent_env = { PAGER: 'cat', TOKEN_CMD: '`echo ok`', EXTRA: '1' }
  h.omo.emit('settings.changed', { section: 'general', name: '', source: 'app' })
  await flushPromises()
  expect(rows(wr)).toEqual(['EXTRA', 'PAGER', 'TOKEN_CMD'])
  expect(wr.find('[data-env-check-result]').exists()).toBe(false)
})
