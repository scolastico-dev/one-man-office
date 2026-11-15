import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, test } from 'vitest'
import { hasHidden, visibleLines, visibleText } from './visibleText'

/** The list of hidden characters the server checks against as well (internal/textsafe). */
const shared = JSON.parse(readFileSync(resolve(import.meta.dirname, '../../../../internal/textsafe/testdata/hidden.json'), 'utf8')) as {
  hidden: [string, string, string][]
  shown: string[]
}

describe('visibleText', () => {
  test('escapes controls and bidi overrides, keeps normal Unicode', () => {
    expect(visibleText('a\nb\tc\rd\u0000e\u001bf\u007fg\u0085h')).toBe('a\\u{A}b\\u{9}c\\u{D}d\\u{0}e\\u{1B}f\\u{7F}g\\u{85}h')
    expect(visibleText('evil\u202Etxt.exe')).toBe('evil\\u{202E}txt.exe')
    for (const cp of [0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069, 0x200e, 0x200f, 0x061c, 0x2028, 0x2029]) {
      expect(visibleText(`x${String.fromCodePoint(cp)}y`)).toBe(`x\\u{${cp.toString(16).toUpperCase()}}y`)
    }
    expect(visibleText('Übersetzung/日本語/emoji 🐌/عربى/ñ')).toBe('Übersetzung/日本語/emoji 🐌/عربى/ñ')
    expect(visibleText('')).toBe('')
  })

  test('keeps spaces and printable ASCII as they are', () => {
    const ascii = Array.from({ length: 95 }, (_, i) => String.fromCharCode(32 + i)).join('')
    expect(visibleText(ascii)).toBe(ascii)
  })

  test('escapes exactly the characters of the shared list, over all of Unicode', () => {
    const hidden = new Set<number>()
    for (const [from, to] of shared.hidden) for (let c = parseInt(from, 16); c <= parseInt(to, 16); c++) hidden.add(c)
    for (const s of shared.shown) expect(hidden.has(parseInt(s, 16))).toBe(false)
    const wrong: string[] = []
    for (let c = 0; c <= 0x10ffff; c++) {
      if (c >= 0xd800 && c <= 0xdfff) continue
      const ch = String.fromCodePoint(c)
      const escaped = visibleText(ch) !== ch
      if (escaped !== hidden.has(c) || hasHidden(`a${ch}b`) !== hidden.has(c)) wrong.push(c.toString(16))
    }
    expect(wrong).toEqual([])
  })

  test('escapes invisible characters that hide text', () => {
    expect(visibleText('a\u200Bb\u00ADc\uFEFFd\u2060e')).toBe('a\\u{200B}b\\u{AD}c\\u{FEFF}d\\u{2060}e')
    expect(visibleText(`x${String.fromCodePoint(0xe0041)}y`)).toBe('x\\u{E0041}y')
    // The joiners stay: emoji sequences need them.
    expect(visibleText('👩\u200D💻')).toBe('👩\u200D💻')
  })

  test('visibleLines keeps line breaks and tabs', () => {
    expect(visibleLines('a\tb\nc\u202Ed\re\u001b')).toBe('a\tb\nc\\u{202E}d\\u{D}e\\u{1B}')
  })
})
