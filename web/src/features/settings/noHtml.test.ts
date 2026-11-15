import { expect, test } from 'vitest'

// Settings pages show names, paths, prompts and messages that agents can
// write; they are rendered as text only, never as HTML.
const sources = import.meta.glob(['./**/*.{vue,ts}', '!./**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

test('no v-html anywhere under features/settings', () => {
  expect(Object.keys(sources).length).toBeGreaterThan(10)
  const found = Object.entries(sources)
    .filter(([, text]) => /\bv-html\b|\binnerHTML\b|\bouterHTML\b|insertAdjacentHTML|renderMarkdown/.test(text))
    .map(([file]) => file)
  expect(found).toEqual([])
})
