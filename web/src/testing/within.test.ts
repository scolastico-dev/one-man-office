import { posix, win32 } from 'node:path'
import { describe, expect, it } from 'vitest'
import { isBelow } from '../../e2e/support/within'

describe('isBelow', () => {
  it('accepts a descendant by the separators of each platform', () => {
    expect(isBelow('/tmp/omo', '/tmp/omo/work/plugin.json', posix)).toBe(true)
    expect(isBelow('C:\\Users\\a\\AppData\\Local\\Temp', 'C:\\Users\\a\\AppData\\Local\\Temp\\omo-1\\work', win32)).toBe(true)
    expect(isBelow('C:\\Temp', 'c:\\temp\\omo', win32)).toBe(true)
  })
  it('refuses the folder itself, escapes, siblings with a shared prefix and other drives', () => {
    expect(isBelow('/tmp/omo', '/tmp/omo', posix)).toBe(false)
    expect(isBelow('/tmp/omo', '/tmp/omo/../etc', posix)).toBe(false)
    expect(isBelow('/tmp/omo', '/tmp/omo2/x', posix)).toBe(false)
    expect(isBelow('/tmp/omo', '/tmp', posix)).toBe(false)
    expect(isBelow('C:\\Temp', 'C:\\Temp\\..\\Windows', win32)).toBe(false)
    expect(isBelow('C:\\Temp', 'C:\\Temp2\\x', win32)).toBe(false)
    expect(isBelow('C:\\Temp', 'D:\\Temp\\x', win32)).toBe(false)
    expect(isBelow('C:\\Temp', 'C:\\Temp', win32)).toBe(false)
  })
  it('a name that merely begins with two dots is inside', () => {
    expect(isBelow('/tmp/omo', '/tmp/omo/..hidden/x', posix)).toBe(true)
    expect(isBelow('C:\\Temp', 'C:\\Temp\\..hidden', win32)).toBe(true)
  })
  it('the old text test fails on Windows paths', () => {
    // Pins why the helper exists: the hard-coded separator rejects an owned descendant.
    const tmp = 'C:\\Temp', root = 'C:\\Temp\\omo'
    expect(root.startsWith(tmp + '/')).toBe(false)
    expect(isBelow(tmp, root, win32)).toBe(true)
  })
})
