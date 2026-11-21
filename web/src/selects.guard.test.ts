import { expect, test } from 'vitest'

// Every source file of the app except tests, as text.
const sources = import.meta.glob(['/src/**/*.{vue,ts,css}', '!/src/**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

const css = sources['/src/styles/main.css'] ?? ''

/** The body of the first rule whose selector list is exactly `selector`. */
function rule(text: string, selector: string): string {
  const at = text.indexOf(`${selector} {`)
  if (at < 0) return ''
  return text.slice(at, text.indexOf('}', at))
}

test('the guard sees the files it scans', () => {
  expect(css).toContain('.omo-input')
  expect(Object.keys(sources).filter((f) => f.endsWith('.vue')).length).toBeGreaterThan(50)
})

test.each(Object.entries(sources))('every select of the app uses the shared field look: %s', (file, text) => {
  const found: string[] = []
  if (!file.endsWith('.vue')) return
  for (const m of text.matchAll(/<select\b[^>]*>/g)) {
    if (!/\bclass="[^"]*\bomo-input\b/.test(m[0])) found.push(`${file}: ${m[0]}`)
  }
  expect(found).toEqual([])
})

test('the arrow of a select keeps the same distance from the right edge as the text from the left', () => {
  const input = rule(css, '.omo-input')
  const select = rule(css, 'select.omo-input')
  // One distance for both sides, set once and followed by the arrow.
  expect(input).toMatch(/--omo-input-pad-x:/)
  expect(input).toMatch(/padding:[^;]*var\(--omo-input-pad-x\)/)
  expect(select).toMatch(/appearance:\s*none/)
  expect(select).toMatch(/background-position:[^;]*right var\(--omo-input-pad-x\)/)
  expect(select).toMatch(/padding-right:[^;]*var\(--omo-input-pad-x\)/)
})

test.each(Object.entries(sources))('no component sets the side padding of a select itself: it changes the distance through --omo-input-pad-x: %s', (file, text) => {
  const found: string[] = []
  if (!file.endsWith('.vue')) return
  const classes = new Set<string>()
  for (const m of text.matchAll(/<select\b[^>]*\bclass="([^"]*)"/g)) for (const c of m[1]!.split(/\s+/)) if (c && c !== 'omo-input') classes.add(c)
  const style = /<style[^>]*>([\s\S]*?)<\/style>/.exec(text)?.[1] ?? ''
  for (const c of classes) {
    for (const m of style.matchAll(new RegExp(`\\.${c}\\b[^{]*\\{([^}]*)\\}`, 'g'))) {
      if (/(^|[\s;])padding(-inline|-left|-right)?\s*:/.test(m[1]!)) found.push(`${file}: .${c}`)
    }
  }
  expect(found).toEqual([])
})
