import { expect, test } from 'vitest'
import { SETTINGS_SECTIONS_IN_ORDER } from '../../locations'
import { SETTINGS_SECTIONS } from './settingsSections'

// Every source file of the app except tests, as text.
const sources = import.meta.glob(['/src/**/*.{vue,ts}', '!/src/**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

test('the section list has the eleven sections in order', () => {
  expect(SETTINGS_SECTIONS.map((s) => s.id)).toEqual(['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'environment', 'limits', 'review', 'plugins', 'general'])
})

test('the sections are listed in the order of the section list, each with a page', async () => {
  expect(SETTINGS_SECTIONS.map((s) => s.id)).toEqual([...SETTINGS_SECTIONS_IN_ORDER])
  for (const s of SETTINGS_SECTIONS) {
    expect(s.label).not.toBe('')
    expect(s.description).not.toMatch(/\.$/)
    const page = await s.component()
    expect(page).toBeTruthy()
  }
})

test('the settings are loaded only when shown: nothing outside them imports their code statically', () => {
  const statically = /^\s*import\s[^;]*?from\s+['"]([^'"]+)['"]/gm
  const found: string[] = []
  for (const [file, text] of Object.entries(sources)) {
    const inside = file.startsWith('/src/features/settings/')
    for (const m of text.matchAll(statically)) {
      const spec = m[1]!
      const settingsCode = /(^|\/)features\/settings\//.test(spec) || (!inside && /^\.\.?\/(\.\.\/)*settings\//.test(spec))
      if (!inside && settingsCode) found.push(`${file}: ${spec}`)
      // The view and the section list import pages only dynamically.
      if (inside && /\/(SettingsView\.vue|settingsSections\.ts)$/.test(file) && /Page\.vue$/.test(spec)) found.push(`${file}: ${spec}`)
    }
  }
  expect(found).toEqual([])
  expect(sources['/src/router.ts']).toContain("import('./features/settings/SettingsView.vue')")
})
