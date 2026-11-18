import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import type { PluginInfo } from '../../../api/types'
import PluginList from './PluginList.vue'

const info = (name: string): PluginInfo => ({ name, version: '1.0.0', description: '', state: 'ready', message: '', commit: 'abc', last_event: '', last_run_at: null, hooks: [], buttons: [], frontend: {}, config_fields: [], content_id: '' })

test('each row is edited with Edit, like scripts and skill sets; the open one says Close', () => {
  const w = mount(PluginList, { props: { plugins: [info('a'), info('b')], settings: [], selected: 'b' } })
  expect(w.findAll('[data-plugin-toggle]').map((b) => b.text())).toEqual(['Edita', 'Closeb'])
})

test('a plugin that is off and has no installed files says not installed', () => {
  const off = (commit: string): PluginInfo => ({ ...info('x'), state: 'disabled', commit, version: '' })
  const w = mount(PluginList, { props: { plugins: [off('')], settings: [], selected: null } })
  expect(w.get('[data-plugin-state]').text()).toBe('not installed')
  const installed = mount(PluginList, { props: { plugins: [off('abc')], settings: [], selected: null } })
  expect(installed.get('[data-plugin-state]').text()).toBe('off')
})
