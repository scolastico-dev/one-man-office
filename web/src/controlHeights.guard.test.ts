import { expect, test } from 'vitest'

// Controls that share a row are equally tall because every control takes its
// height from one token (--omo-control-h, or --omo-control-h-sm in a compact
// row): main.css gives buttons, fields and selects that height, Toggle too.
// A component that sets the height of a control itself (a Browse… button made
// as tall as the field beside it by a number) breaks the rule the next time
// either changes. The browser test e2e/control-heights.spec.ts measures the
// rows; this one keeps the sources on the token.

const sources = import.meta.glob(['/src/**/*.{vue,css}'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

const css = sources['/src/styles/main.css'] ?? ''

/** Elements that are controls: native ones and the components that render one. */
const CONTROL_TAG = /<(button|input|select|a|UiButton|IconButton|Toggle|SearchPicker|RouterLink)\b[^>]*?\bclass="([^"]*)"/g
/** A declaration that sets how tall a box is. */
const HEIGHT = /(^|[\s;{])((?:min-|max-)?height|padding-(?:top|bottom|block))\s*:\s*([^;}]+)/g

/**
 * Control classes that may set a height of their own, with the reason. They
 * are not controls on a row with others: rows of a list, tabs.
 */
const ALLOWED: Record<string, string> = {
  picker__folder: 'a folder of the folder list: a row of the list, as tall as a field so a tap finds it',
  tabs__tab: 'a tab: tabs share a line only with each other',
  switcher__tab: "a column tab of the phone board: 48 px for the thumb, only beside the other column tabs",
  frow__value: "a ticket field's value: on touch screens 44 px for the finger, alone on its line",
  frow__change: "a ticket field's Change button: on touch screens 44 px for the finger, beside the value's text only",
}

/** The body of the first rule whose selector list ends in `selector`. */
function rule(text: string, selector: string): string {
  const at = text.indexOf(`${selector} {`)
  if (at < 0) return ''
  return text.slice(at, text.indexOf('}', at))
}

test('the guard sees the files it scans', () => {
  expect(css).toContain('--omo-control-h')
  expect(Object.keys(sources).filter((f) => f.endsWith('.vue')).length).toBeGreaterThan(50)
})

test('buttons, fields, selects and switches take their height from the control token', () => {
  expect(rule(css, '.omo-btn')).toMatch(/min-height:\s*var\(--omo-control-h\)/)
  expect(rule(css, 'input.omo-input')).toMatch(/height:\s*var\(--omo-control-h\)/)
  expect(rule(css, '.omo-btn--icon')).toMatch(/width:\s*var\(--omo-control-h\)/)
  expect(rule(css, '.omo-btn--sm')).toMatch(/--omo-control-h:\s*var\(--omo-control-h-sm\)/)
  expect(rule(css, '.omo-input--sm')).toMatch(/--omo-control-h:\s*var\(--omo-control-h-sm\)/)
  expect(rule(css, '.omo-compact')).toMatch(/--omo-control-h:\s*var\(--omo-control-h-sm\)/)
  const toggle = sources['/src/components/ui/Toggle.vue'] ?? ''
  expect(rule(toggle, '.toggle')).toMatch(/min-height:\s*var\(--omo-control-h\)/)
})

test.each(Object.entries(sources))('no component sets the height of a control by a number of its own: %s', (file, text) => {
  const found: string[] = []
  if (!file.endsWith('.vue')) return
  const classes = new Set<string>()
  for (const m of text.matchAll(CONTROL_TAG)) for (const c of m[2]!.split(/\s+/)) if (c && !c.startsWith('omo-') && !(c in ALLOWED)) classes.add(c)
  const style = [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
  for (const c of classes) {
    for (const m of style.matchAll(new RegExp(`\\.${c}(?![\\w-])[^{}]*\\{([^}]*)\\}`, 'g'))) {
      for (const d of m[1]!.matchAll(HEIGHT)) {
        const value = d[3]!.trim()
        if (/var\(--omo-control-h(-sm)?\)/.test(value) || /^(0|auto|none|100%)$/.test(value)) continue
        found.push(`${file}: .${c} { ${d[2]}: ${value} }`)
      }
    }
  }
  expect(found).toEqual([])
})
