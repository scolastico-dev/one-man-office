import { describe, expect, test } from 'vitest'
import { visibleText } from './visibleText'

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
})
