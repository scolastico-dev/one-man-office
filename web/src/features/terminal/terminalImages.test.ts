import { describe, expect, test } from 'vitest'
import { isImageFile, pastedPaths, shellQuote } from './terminalImages'

describe('isImageFile', () => {
  const file = (name: string, type: string) => new File(['x'], name, { type })

  test('the types omo stores', () => {
    for (const type of ['image/png', 'image/jpeg', 'image/gif', 'image/webp']) expect(isImageFile(file('a', type)), type).toBe(true)
  })

  test('a file without a type by its extension', () => {
    expect(isImageFile(file('shot.PNG', ''))).toBe(true)
    expect(isImageFile(file('photo.jpeg', ''))).toBe(true)
    expect(isImageFile(file('notes.txt', ''))).toBe(false)
  })

  test('anything else is refused, SVG too', () => {
    expect(isImageFile(file('notes.txt', 'text/plain'))).toBe(false)
    expect(isImageFile(file('logo.svg', 'image/svg+xml'))).toBe(false)
    expect(isImageFile(file('a.pdf', 'application/pdf'))).toBe(false)
  })
})

describe('shellQuote', () => {
  test('a plain path stays as it is', () => {
    expect(shellQuote('/home/me/.local/omo/images/terminal/0b5e-4f.png')).toBe('/home/me/.local/omo/images/terminal/0b5e-4f.png')
    expect(shellQuote('C:\\Users\\me\\AppData\\Roaming\\omo\\images\\terminal\\a.png')).toBe(
      'C:\\Users\\me\\AppData\\Roaming\\omo\\images\\terminal\\a.png',
    )
  })

  test('spaces and shell characters are single-quoted as a file manager pastes them', () => {
    expect(shellQuote('/Users/me/Library/Application Support/omo/a.png')).toBe("'/Users/me/Library/Application Support/omo/a.png'")
    expect(shellQuote("/home/o'neil/a.png")).toBe("'/home/o'\\''neil/a.png'")
    expect(shellQuote('/tmp/$x/a.png')).toBe("'/tmp/$x/a.png'")
  })
})

describe('pastedPaths', () => {
  test('separated by one space, no line break at the end', () => {
    expect(pastedPaths(['/a/1.png', '/b c/2.png'])).toBe("/a/1.png '/b c/2.png'")
    expect(pastedPaths(['/a/1.png'])).toBe('/a/1.png')
  })
})
