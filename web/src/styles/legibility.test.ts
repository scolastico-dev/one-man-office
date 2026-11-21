import { describe, expect, test } from 'vitest'

// Every source file of the app except tests, as text.
const sources = import.meta.glob(['/src/**/*.{vue,ts,css}', '!/src/**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

const MIN_PX = 12
const PX_PER_REM = 16

function toPx(value: number, unit: string): number {
  return unit === 'px' ? value : value * PX_PER_REM
}

/** Font sizes below the minimum, as "file: match". */
function smallFontSizes(file: string, text: string): string[] {
  const found: string[] = []
  const patterns = [
    // font-size: 11px / .7rem / 0.7em, the font shorthand, and size tokens
    /font-size\s*:\s*(\d*\.?\d+)(px|rem|em)\b/g,
    /\bfont\s*:\s*(?:[\w-]+\s+)*(\d*\.?\d+)(px|rem|em)\b/g,
    /--(?:omo-)?text-[\w-]*?\s*:\s*(\d*\.?\d+)(px|rem|em)\b/g,
    // Tailwind arbitrary sizes: text-[11px], text-[0.7rem]
    /\btext-\[(\d*\.?\d+)(px|rem|em)\]/g,
  ]
  for (const re of patterns) {
    for (const m of text.matchAll(re)) {
      if (toPx(Number(m[1]), m[2]!) < MIN_PX) found.push(`${file}: ${m[0]}`)
    }
  }
  for (const m of text.matchAll(/(?<![\w-])text-(?:\dxs|xxs|tiny|micro)(?![\w-])/g)) {
    found.push(`${file}: ${m[0]}`)
  }
  return found
}

/** Rounded corners, as "file: match". */
function roundedCorners(file: string, text: string): string[] {
  const found: string[] = []
  for (const m of text.matchAll(/(?<![\w-])(?:[a-z-]+:)*rounded(?:-[\w[\].%/-]+)?(?![\w-])/g)) {
    if (!/rounded-none$/.test(m[0])) found.push(`${file}: ${m[0]}`)
  }
  for (const m of text.matchAll(/border(?:-[a-z]+)*-radius\s*:\s*([^;}"]+)/g)) {
    if (!/^0(px)?\s*(!important)?$/.test(m[1]!.trim())) found.push(`${file}: ${m[0]}`)
  }
  return found
}

const FAMILY_NAMES = /JetBrains Mono|Cascadia|SFMono|Menlo|Consolas|Liberation Mono|ui-monospace|\bmonospace\b|sans-serif|\bserif\b/

/** Font families named anywhere but the --omo-font token and the @font-face rules. */
function strayFontFamilies(file: string, text: string): string[] {
  const found: string[] = []
  const withoutFaces = text.replace(/@font-face\s*{[^}]*}/g, '')
  for (const line of withoutFaces.split('\n')) {
    if (/^\s*--omo-font\s*:/.test(line)) continue
    if (FAMILY_NAMES.test(line)) found.push(`${file}: ${line.trim()}`)
    const decl = /font-family\s*:\s*([^;}]+)/.exec(line)
    if (decl && !/^(var\(--omo-font\)|inherit)$/.test(decl[1]!.trim())) found.push(`${file}: ${line.trim()}`)
    if (/(?<![\w-])font-\[/.test(line)) found.push(`${file}: ${line.trim()}`)
  }
  return found
}

const COLOUR_FUNCTIONS = /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(/
const HEX = /#[0-9a-fA-F]{3,8}\b/
const TAILWIND_PALETTE =
  /(?<![\w-])(?:[a-z-]+:)*(?:bg|text|border|outline|ring|fill|stroke|from|to|via|decoration|divide|accent|caret|shadow)-(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|black|white)(?![\w-])/

/** Colour values written outside tokens.css. */
function colourLiterals(file: string, text: string): string[] {
  if (file.endsWith('/styles/tokens.css')) return []
  const found: string[] = []
  // Style code: whole CSS files, and the <style> blocks and attributes of components.
  const styleParts = file.endsWith('.css')
    ? [text]
    : [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]!)
  for (const part of styleParts) {
    for (const line of part.split('\n')) {
      if (HEX.test(line) || COLOUR_FUNCTIONS.test(line)) found.push(`${file}: ${line.trim()}`)
    }
  }
  if (file.endsWith('.vue')) {
    const template = text.replace(/<style[^>]*>[\s\S]*?<\/style>/g, '')
    for (const line of template.split('\n')) {
      if (/(?:fill|stroke|color)="#/.test(line) || /-\[#/.test(line) || TAILWIND_PALETTE.test(line)) {
        found.push(`${file}: ${line.trim()}`)
      }
    }
  }
  return found
}

describe('the checks recognise what they look for', () => {
  test('small font sizes', () => {
    const bad = 'a { font-size: 11px } b { font: 600 .7rem/1 x } <p class="text-[10px] text-2xs">'
    expect(smallFontSizes('x', bad)).toHaveLength(4)
    expect(smallFontSizes('x', 'a { font-size: 12px } <p class="text-xs text-[0.8rem]">')).toEqual([])
  })
  test('rounded corners', () => {
    expect(roundedCorners('x', '<div class="rounded hover:rounded-lg rounded-none"> a { border-radius: 4px }')).toHaveLength(3)
    expect(roundedCorners('x', 'a { border-radius: 0 }')).toEqual([])
  })
  test('font families', () => {
    expect(strayFontFamilies('x', 'a { font-family: Menlo }\n<p class="font-[Arial]">')).toHaveLength(3)
    expect(strayFontFamilies('x', 'a { font-family: var(--omo-font) }')).toEqual([])
  })
  test('colour literals', () => {
    expect(colourLiterals('x.css', 'a { color: #fff }\nb { color: rgb(0 0 0) }')).toHaveLength(2)
    expect(colourLiterals('x.vue', '<p class="text-red-500 bg-[#000]"></p>')).toHaveLength(1)
    expect(colourLiterals('x.vue', '<p class="text-fg">#12</p>')).toEqual([])
  })
})

describe('legibility of web/src', () => {
  test('the scan sees the app sources', () => {
    expect(Object.keys(sources)).toContain('/src/styles/tokens.css')
  })
  test.each(Object.entries(sources))('no font size below 12px: %s', (file, text) => {
    expect(smallFontSizes(file, text), file).toEqual([])
  })
  test.each(Object.entries(sources))('no border-radius except 0: %s', (file, text) => {
    expect(roundedCorners(file, text), file).toEqual([])
  })
  test.each(Object.entries(sources))('the font family is named only in --omo-font: %s', (file, text) => {
    expect(strayFontFamilies(file, text), file).toEqual([])
  })
  test.each(Object.entries(sources))('colours come from tokens only: %s', (file, text) => {
    expect(colourLiterals(file, text), file).toEqual([])
  })
})
