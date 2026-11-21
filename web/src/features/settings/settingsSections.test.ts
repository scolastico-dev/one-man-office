import { expect, test } from 'vitest'
import { SETTINGS_SECTIONS_IN_ORDER } from '../../locations'
import { SETTINGS_SECTIONS } from './settingsSections'

// Every source file of the app except tests, as text.
const sources = import.meta.glob(['/src/**/*.{vue,ts}', '!/src/**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

test('the section list has the thirteen sections in order', () => {
  expect(SETTINGS_SECTIONS.map((s) => s.id)).toEqual(['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'mcp', 'environment', 'limits', 'review', 'plugins', 'general', 'backup'])
})

test('the sections are listed in the order of the section list', () => {
  expect(SETTINGS_SECTIONS.map((s) => s.id)).toEqual([...SETTINGS_SECTIONS_IN_ORDER])
})

// Lazy chunk compilation is independent per section; do not share one timeout
// across all pages on a loaded runner.
test.each(SETTINGS_SECTIONS)('$id has a label, description and page', async (s) => {
  expect(s.label).not.toBe('')
  expect(s.description).not.toMatch(/\.$/)
  expect(await s.component()).toBeTruthy()
})

test.each(Object.entries(sources))('the settings are loaded only when shown: nothing outside them imports their code statically: %s', (file, text) => {
  const statically = /^\s*import\s[^;]*?from\s+['"]([^'"]+)['"]/gm
  const found: string[] = []
  const inside = file.startsWith('/src/features/settings/')
  for (const m of text.matchAll(statically)) {
    const spec = m[1]!
    const settingsCode = /(^|\/)features\/settings\//.test(spec) || (!inside && /^\.\.?\/(\.\.\/)*settings\//.test(spec))
    if (!inside && settingsCode) found.push(`${file}: ${spec}`)
    // The view and the section list import pages only dynamically.
    if (inside && /\/(SettingsView\.vue|settingsSections\.ts)$/.test(file) && /Page\.vue$/.test(spec)) found.push(`${file}: ${spec}`)
  }
  expect(found).toEqual([])
})

test('the router loads settings dynamically', () => {
  expect(sources['/src/router.ts']).toContain("import('./features/settings/SettingsView.vue')")
})
