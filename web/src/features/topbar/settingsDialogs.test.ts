import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { harness, mountWith } from '../create/dialogTesting'
import { useCreateTicket } from '../create/useCreateTicket'
import { createMemoryHistory, createRouter } from 'vue-router'
import SetupReport from '../settings/roles/SetupReport.vue'
import NewTicketButton from './NewTicketButton.vue'
import SettingsLink from './SettingsLink.vue'
import { useSettingsDialogs } from './settingsDialogs'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  useCreateTicket().close()
  useSettingsDialogs().close()
})

test('the gear goes straight to the settings, keeping the board filters; nothing opens in between', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  await router.push('/12?project=demo&q=x&tab=diff')
  const w = mount(SettingsLink, { global: { plugins: [router] }, attachTo: document.body })
  mounted = w
  const link = w.get('a')
  expect(link.attributes('aria-label')).toBe('Settings')
  expect(link.attributes('href')).toBe('/settings?project=demo&q=x')
  expect(w.find('[role="menu"]').exists()).toBe(false)
  await link.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings?project=demo&q=x')
  expect(w.find('[role="menu"], dialog').exists()).toBe(false)
})

test('the agent setup says which role has no usable model', async () => {
  const h = await harness()
  h.respond.roles = [
    { name: 'pm', models: ['claude/sonnet'], assignment: 'round_robin' },
    { name: 'developer', models: [], assignment: 'round_robin' },
    { name: 'reviewer', models: ['gone/model'], assignment: 'round_robin' },
  ]
  h.respond.models = [{ id: 'claude/sonnet', profile: 'claude', name: 'sonnet', cli: 'claude', description: '', selectable: true }]
  h.respond.profiles = { profiles: [{ name: 'claude', cli: 'claude', command: 'claude', models: { sonnet: {} } }], errors: [] }
  const w = await mountWith(h, SetupReport, {})
  mounted = w
  await flushPromises()
  const text = w.get('[data-blocking]').text()
  expect(text).toBe('Nothing starts for the developer and reviewer: they have no usable model.')
  expect(w.get('[data-role="pm"]').text()).toContain('claude/sonnet runs claude with the profile claude')
  expect(w.get('[data-role="developer"]').text()).toContain('No model assigned. Nothing starts for the developer until it has one.')
  expect(w.get('[data-role="reviewer"]').text()).toContain('gone/model does not exist')
  expect(w.get('[data-profile="claude"]').text()).toContain('Models: sonnet.')
})

test('the c key opens the create dialog unless a field has the key or a dialog is open', async () => {
  const w = mount(NewTicketButton, { attachTo: document.body })
  mounted = w
  const create = useCreateTicket()
  const press = (target: EventTarget) => target.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', bubbles: true, cancelable: true }))
  const field = document.createElement('input')
  document.body.append(field)
  press(field)
  expect(create.state.open).toBe(false)
  press(document.body)
  expect(create.state.open).toBe(true)
  field.remove()
})
