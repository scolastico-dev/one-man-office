import { describe, expect, test } from 'vitest'
import { LINE_LIMIT, lineText, parsePatch } from './parsePatch'

const HEAD = 'diff --git a/x.ts b/x.ts\nindex 1111111..2222222 100644\n--- a/x.ts\n+++ b/x.ts\n'

describe('parsePatch', () => {
  test('line numbers, no-newline note, a content line starting with --- or diff --git stays content', () => {
    const patch =
      HEAD +
      '@@ -3,4 +3,5 @@ function x() {\n' +
      ' keep\n' +
      '---- a/not-a-header\n' +
      '+diff --git a/y b/y\n' +
      '++++ b/also-content\n' +
      ' last\n' +
      '-old end\n' +
      '\\ No newline at end of file\n' +
      '+new end\n' +
      '\\ No newline at end of file\n' +
      '@@ -20 +21 @@\n' +
      '-one\n' +
      '+uno\n'
    const p = parsePatch(patch)
    expect(p.cut).toBe(false)
    expect(p.hunks).toHaveLength(2)
    const [a, b] = p.hunks
    expect(a!.header).toBe('@@ -3,4 +3,5 @@ function x() {')
    expect(a!.oldStart).toBe(3)
    expect(a!.newStart).toBe(3)
    expect(a!.lines).toEqual([
      { kind: 'ctx', text: 'keep', old: 3, new: 3 },
      { kind: 'del', text: '--- a/not-a-header', old: 4 },
      { kind: 'add', text: 'diff --git a/y b/y', new: 4 },
      { kind: 'add', text: '+++ b/also-content', new: 5 },
      { kind: 'ctx', text: 'last', old: 5, new: 6 },
      { kind: 'del', text: 'old end', old: 6 },
      { kind: 'note', text: 'No newline at end of file' },
      { kind: 'add', text: 'new end', new: 7 },
      { kind: 'note', text: 'No newline at end of file' },
    ])
    // A count left out means one line.
    expect(b!.oldStart).toBe(20)
    expect(b!.newStart).toBe(21)
    expect(b!.lines).toEqual([
      { kind: 'del', text: 'one', old: 20 },
      { kind: 'add', text: 'uno', new: 21 },
    ])
  })

  test('an empty context line and a new file starting at 0', () => {
    const p = parsePatch('@@ -0,0 +1,3 @@\n+a\n+\n+c\n')
    expect(p.hunks[0]!.lines.map((l) => [l.kind, l.text, l.new])).toEqual([
      ['add', 'a', 1],
      ['add', '', 2],
      ['add', 'c', 3],
    ])
    const q = parsePatch('@@ -1,3 +1,3 @@\n a\n\n-b\n+B\n')
    expect(q.cut).toBe(false)
    expect(q.hunks[0]!.lines[1]).toEqual({ kind: 'ctx', text: '', old: 2, new: 2 })
  })

  test('a cut patch ends without throwing and sets cut', () => {
    const full = '@@ -1,5 +1,5 @@\n a\n b\n-c\n+C\n d\n e\n'
    for (let end = 0; end < full.length; end++) {
      const p = parsePatch(full.slice(0, end))
      expect(p.hunks.length).toBeLessThanOrEqual(1)
      if (p.hunks.length === 1) expect(p.cut).toBe(true)
    }
    expect(parsePatch(full).cut).toBe(false)
    const cut = parsePatch('@@ -1,5 +1,5 @@\n a\n b\n')
    expect(cut.cut).toBe(true)
    expect(cut.hunks[0]!.lines).toHaveLength(2)
  })

  test('nothing to read gives no hunks', () => {
    expect(parsePatch('')).toEqual({ hunks: [], cut: false })
    expect(parsePatch(HEAD)).toEqual({ hunks: [], cut: false })
    expect(parsePatch('Binary files a/x and b/x differ\n')).toEqual({ hunks: [], cut: false })
  })
})

describe('lineText', () => {
  test('controls, bidi overrides and HTML are shown as text, tabs stay', () => {
    const rlo = '‮'
    const { text, cut } = lineText(`\tif (a)${rlo} {\u001b[31m<img src=x onerror=alert(1)>\u0000\r`)
    expect(cut).toBe(0)
    expect(text).toBe('\tif (a)\\u{202E} {\\u{1B}[31m<img src=x onerror=alert(1)>\\u{0}\\u{D}')
    expect(text).not.toContain(rlo)
  })

  test('a long line is cut and says by how much', () => {
    const long = 'x'.repeat(LINE_LIMIT + 25)
    const r = lineText(long)
    expect(r.text).toHaveLength(LINE_LIMIT)
    expect(r.cut).toBe(25)
    // A surrogate pair at the edge is not split.
    const pair = 'y'.repeat(LINE_LIMIT - 1) + '🐌' + 'z'
    const s = lineText(pair)
    expect(s.text.endsWith('y')).toBe(true)
    expect(s.text).not.toMatch(/[\uD800-\uDBFF]$/)
  })
})
