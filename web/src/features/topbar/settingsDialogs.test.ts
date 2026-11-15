import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { harness, mountWith } from '../create/dialogTesting'
import { useCreateTicket } from '../create/useCreateTicket'
import { createMemoryHistory, createRouter } from 'vue-router'
import SetupReport from '../settings/roles/SetupReport.vue'
import NewTicketButton from './NewTicketButton.vue'
import ThemeDialog from './ThemeDialog.vue'
import { settingsMenu, useSettingsDialogs } from './settingsMenu'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  document.documentElement.removeAttribute('data-theme')
  window.localStorage.removeItem('omo.theme')
  useCreateTicket().close()
  useSettingsDialogs().close()
})

test('the gear menu opens the settings pages, keeping the board filters, and the theme dialog', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  await router.push('/12?project=demo&q=x&tab=diff')
  const entries = settingsMenu(router)
  expect(entries.map((e) => e.label)).toEqual(['Settings', 'Projects', 'Agent setup', 'Theme'])
  const opens = async (i: number) => {
    entries[i]!.open()
    await flushPromises()
    return router.currentRoute.value.fullPath
  }
  expect(await opens(0)).toBe('/settings/projects?project=demo&q=x')
  expect(await opens(2)).toBe('/settings/roles?project=demo&q=x')
  expect(await opens(1)).toBe('/settings/projects?project=demo&q=x')
  expect(useSettingsDialogs().state.open).toBeNull()
  entries[3]!.open()
  expect(useSettingsDialogs().state.open).toBe('theme')
})

test('the theme choice applies at once, is remembered and marks the current choice', async () => {
  const w = mount(ThemeDialog, { props: { open: true }, attachTo: document.body })
  mounted = w
  const radios = w.findAll('input[type="radio"]')
  expect(radios.map((r) => r.attributes('value'))).toEqual(['system', 'dark', 'light'])
  expect((radios[0]!.element as HTMLInputElement).checked).toBe(true)
  await radios[2]!.setValue(true)
  expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  expect(window.localStorage.getItem('omo.theme')).toBe('light')
  await radios[0]!.setValue(true)
  expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  expect(window.localStorage.getItem('omo.theme')).toBe('system')
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
