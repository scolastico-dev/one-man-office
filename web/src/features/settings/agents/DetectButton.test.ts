import { flushPromises } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { mountIn, setup } from './agentsTesting'
import DetectButton from './DetectButton.vue'

test('detect lists what was created and what was skipped', async () => {
  const s = await setup()
  s.state.write = () => ({
    found: [
      { cli: 'claude', command: 'claude', path: '/usr/bin/claude' },
      { cli: 'codex', command: 'codex', path: '/usr/bin/codex' },
    ],
    created: ['claude'],
    assigned: ['ceo', 'developer'],
    skipped: ['codex: the profile codex exists'],
  })
  const w = await mountIn(s, DetectButton, {})
  expect(w.get('[data-detect-explained]').text()).toContain("skips the program's permission prompts")
  expect(w.find('[data-detect-result]').exists()).toBe(false)
  await w.get('[data-detect-button]').trigger('click')
  await flushPromises()
  expect(s.sent[0]).toMatchObject({ method: 'POST', path: '/api/agent-setup/detect' })
  expect(w.findAll('[data-detect-found]').map((x) => x.text())).toEqual(['claude: /usr/bin/claude', 'codex: /usr/bin/codex'])
  expect(w.get('[data-detect-created]').text()).toBe('Profiles written: claude.')
  expect(w.get('[data-detect-assigned]').text()).toBe('Roles that got models: ceo, developer.')
  expect(w.findAll('[data-detect-skipped]').map((x) => x.text())).toEqual(['codex: the profile codex exists'])
})

test('detect with nothing found says so', async () => {
  const s = await setup()
  s.state.write = () => ({ found: [], created: [], assigned: [], skipped: [] })
  const w = await mountIn(s, DetectButton, {})
  await w.get('[data-detect-button]').trigger('click')
  await flushPromises()
  expect(w.find('[data-detect-found-none]').exists()).toBe(true)
  expect(w.find('[data-detect-created-none]').exists()).toBe(true)
})

