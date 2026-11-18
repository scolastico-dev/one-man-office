import { expect, test } from 'vitest'
import type { PluginHook } from '../../../api/types'
import {
  CONFIG_PARSE_MESSAGE,
  configText,
  eventNoun,
  fileOfPlugin,
  hookWords,
  installResult,
  manualActions,
  parseConfig,
  suggestName,
} from './pluginForms'

const hook = (event: string, o: Partial<PluginHook> = {}): PluginHook => ({ event, name: '', description: '', scope: '', interval: '', ...o })

test('hooks described in words', () => {
  expect(hookWords(hook('ticket_moved'))).toBe('When a ticket is moved')
  expect(hookWords(hook('comment_written'))).toBe('When a comment is written')
  expect(hookWords(hook('timer', { interval: '5m0s' }))).toBe('Every 5m0s')
  expect(hookWords(hook('manual', { name: 'mark', description: 'Mark it', scope: 'ticket' }))).toBe('Button on a ticket: Mark it')
  expect(hookWords(hook('manual', { name: 'sum', description: 'Write a summary', scope: 'global' }))).toBe('Button here: Write a summary')
  expect(hookWords(hook('something_new'))).toBe('On something_new')
  expect(eventNoun('ticket_moved')).toBe('a moved ticket')
  expect(eventNoun('manual')).toBe('a button')
  expect(hookWords(hook('omo_paused'))).toBe('When omo is paused')
  expect(hookWords(hook('omo_resumed'))).toBe('When the pause ends')
  expect(eventNoun('omo_paused')).toBe('the pause')
  expect(eventNoun('omo_resumed')).toBe('the end of the pause')
})

test('manual actions by scope, global by default', () => {
  const info = { hooks: [hook('manual', { name: 'a', description: 'A' }), hook('manual', { name: 'b', description: 'B', scope: 'ticket' }), hook('ticket_moved')] }
  expect(manualActions(info, 'global').map((h) => h.name)).toEqual(['a'])
  expect(manualActions(info, 'ticket').map((h) => h.name)).toEqual(['b'])
})

test('the configuration is edited as a JSON object; anything else gives the parse message', () => {
  expect(configText({ a: 1, b: { c: 'x' } })).toBe('{\n  "a": 1,\n  "b": {\n    "c": "x"\n  }\n}')
  expect(parseConfig('{"a": [1, null]}')).toEqual({ ok: true, value: { a: [1, null] } })
  for (const bad of ['', '{', '[]', 'null', '"x"', '3', '{"a": 1,}']) expect(parseConfig(bad)).toEqual({ ok: false, message: CONFIG_PARSE_MESSAGE })
})

test('a name is suggested from the source as typed', () => {
  expect(suggestName('https://example.invalid/me/omo-ticket-log.git')).toBe('omo-ticket-log')
  expect(suggestName('git@example.invalid:me/tools.git/')).toBe('tools')
  expect(suggestName('/home/me/code/my plugin')).toBe('my-plugin')
  expect(suggestName('/x/.hidden')).toBe('hidden')
  expect(suggestName('')).toBe('')
})

test('install results and commits in words', () => {
  expect(installResult({ commit: '9c2e4f1aa', state: 'ready', message: '' }, true)).toEqual({ ok: true, text: 'Updated; now at 9c2e4f1.' })
  expect(installResult({ commit: '', state: 'ready', message: '' }, false)).toEqual({ ok: true, text: 'Installed.' })
  expect(installResult({ commit: 'abc', state: 'invalid', message: 'boom' }, false)).toEqual({ ok: false, text: 'Installed, but it does not load: boom' })
})

test('the plugin a settings file belongs to', () => {
  expect(fileOfPlugin('/h/omo/plugins/ticket-log.yaml')).toBe('ticket-log')
  expect(fileOfPlugin('/h/omo/skillsets/x.yaml')).toBeNull()
  expect(fileOfPlugin('/h/omo/plugins/x/plugin.json')).toBeNull()
})

test.each(['{"idle":1e400}', '{"nested":{"n":-1e400}}', '{"list":[1e400]}'])('configuration refuses non-finite JSON numbers without converting them to null: %s', (text) => {
  expect(parseConfig(text)).toEqual({ ok: false, message: CONFIG_PARSE_MESSAGE })
})
