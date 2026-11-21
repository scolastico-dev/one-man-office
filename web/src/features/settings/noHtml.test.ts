import { expect, test } from 'vitest'

// Settings pages show names, paths, prompts and messages that agents can
// write; they are rendered as text only, never as HTML.
const sources = import.meta.glob(['./**/*.{vue,ts}', '!./**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

test('the HTML guard sees the settings sources', () => {
  expect(Object.keys(sources).length).toBeGreaterThan(10)
})

test.each(Object.entries(sources))('no v-html anywhere under features/settings: %s', (file, text) => {
  expect(text, file).not.toMatch(/\bv-html\b|\binnerHTML\b|\bouterHTML\b|insertAdjacentHTML|renderMarkdown/)
})
