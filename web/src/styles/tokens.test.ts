import { describe, expect, test } from 'vitest'
import css from './tokens.css?raw'

type Tokens = Record<string, string>

function block(source: string, selector: string): Tokens {
  const start = source.indexOf(`${selector} {`)
  if (start < 0) throw new Error(`no block for ${selector}`)
  const open = source.indexOf('{', start)
  const close = source.indexOf('}', open)
  const tokens: Tokens = {}
  for (const m of source.slice(open + 1, close).matchAll(/--omo-([\w-]+)\s*:\s*([^;]+);/g)) {
    tokens[m[1]!] = m[2]!.trim()
  }
  return tokens
}

function rgb(hex: string): [number, number, number] {
  const m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i.exec(hex)
  if (!m) throw new Error(`not an opaque hex colour: ${hex}`)
  const n = parseInt(m[1]!, 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

function luminance(hex: string): number {
  const [r, g, b] = rgb(hex).map((v) => {
    const c = v / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

const dark = block(css, ':root')
const lightBySystem = block(css, ':root:not([data-theme="dark"])')
const lightByChoice = block(css, ':root[data-theme="light"]')
const colourNames = Object.keys(dark).filter((k) => /^#/.test(dark[k]!))

const themes: [string, Tokens][] = [
  ['dark', dark],
  ['light', lightByChoice],
]

const textTokens = ['fg', 'muted', 'accent', 'ok', 'warn', 'danger']
const plainBackgrounds = ['bg', 'surface', 'raised']
const tintedBackgrounds = ['accent-soft', 'select']

function failures(t: Tokens, pairs: [string, string][], min: number): string[] {
  return pairs
    .map(([fg, bg]) => [fg, bg, contrast(t[fg]!, t[bg]!)] as const)
    .filter(([, , ratio]) => ratio < min)
    .map(([fg, bg, ratio]) => `${fg} on ${bg}: ${ratio.toFixed(2)}`)
}

function cross(a: string[], b: string[]): [string, string][] {
  return a.flatMap((x) => b.map((y) => [x, y] as [string, string]))
}

describe('tokens', () => {
  test('both light blocks are identical and every theme defines every colour', () => {
    expect(lightBySystem).toEqual(lightByChoice)
    const lightColours = Object.keys(lightByChoice).filter((k) => /^#/.test(lightByChoice[k]!))
    expect(lightColours.sort()).toEqual([...colourNames].sort())
  })

  test('body text sizes have a line height of at least 1.4', () => {
    for (const size of ['xs', 'sm', 'base', 'lg', 'xl']) {
      expect(Number(dark[`text-${size}-lh`]), size).toBeGreaterThanOrEqual(1.4)
      expect(parseFloat(dark[`text-${size}`]!), size).toBeGreaterThanOrEqual(12)
    }
  })

  test('the font family is a single token', () => {
    expect(dark.font).toMatch(/^"JetBrains Mono", .*monospace$/)
  })

  test.each(themes)('every text token reaches 4.5:1 on bg, surface and raised in both themes (%s)', (_, t) => {
    expect(failures(t, cross(textTokens, plainBackgrounds), 4.5)).toEqual([])
  })

  test.each(themes)('every text token reaches 4.5:1 on the selection and hover fills (%s)', (_, t) => {
    expect(failures(t, cross(textTokens, tintedBackgrounds), 4.5)).toEqual([])
  })

  test.each(themes)('accent-fg on accent, and fg on accent-soft, select and danger-soft reach 4.5:1 (%s)', (_, t) => {
    const pairs: [string, string][] = [
      ['accent-fg', 'accent'],
      ['accent-fg', 'accent-hover'],
      ['fg', 'accent-soft'],
      ['fg', 'select'],
      ['fg', 'danger-soft'],
      ['muted', 'danger-soft'],
      ['danger', 'danger-soft'],
    ]
    expect(failures(t, pairs, 4.5)).toEqual([])
  })

  test.each(themes)('line-strong reaches 3:1 against bg, surface and raised in both themes (%s)', (_, t) => {
    expect(failures(t, cross(['line-strong'], plainBackgrounds), 3)).toEqual([])
  })

  test.each(themes)('the focus outline (accent) reaches 3:1 against every background (%s)', (_, t) => {
    expect(failures(t, cross(['accent'], [...plainBackgrounds, ...tintedBackgrounds]), 3)).toEqual([])
  })
})
