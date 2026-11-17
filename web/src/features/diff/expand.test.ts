import { describe, expect, test } from 'vitest'
import type { DiffContext } from '../../api/types'
import {
  BASE_CONTEXT,
  contextFor,
  covers,
  expandable,
  expanders,
  knownEnd,
  NOTHING_REVEALED,
  revealFor,
  sameChanges,
  viewRows,
  type Gap,
  type LineRow,
  type ViewRow,
} from './expand'
import { parsePatch } from './parsePatch'

/**
 * The patch git writes for a file of `n` lines "line i" in which the lines
 * in `changed` became "line i changed", with `context` lines of context.
 */
function patchOf(n: number, changed: number[], context: DiffContext): string {
  const c = context === 'all' ? n : context
  const groups: number[][] = []
  for (const k of [...changed].sort((a, b) => a - b)) {
    const last = groups[groups.length - 1]
    if (last && k - last[last.length - 1]! - 1 <= 2 * c) last.push(k)
    else groups.push([k])
  }
  let out = 'diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n'
  for (const g of groups) {
    const from = Math.max(1, g[0]! - c)
    const to = Math.min(n, g[g.length - 1]! + c)
    const count = to - from + 1
    out += `@@ -${from},${count} +${from},${count} @@ fn()\n`
    for (let i = from; i <= to; i++) out += g.includes(i) ? `-line ${i}\n+line ${i} changed\n` : ` line ${i}\n`
  }
  return out
}

const hunksOf = (n: number, changed: number[], context: DiffContext = BASE_CONTEXT) => parsePatch(patchOf(n, changed, context)).hunks
const wider = (n: number, changed: number[], context: DiffContext) => ({ parsed: parsePatch(patchOf(n, changed, context)), context })

/** The rows in short: gap rows as "gap <place> <from>+<count>", hunk rows as their text, lines as "<kind> <old>/<new>". */
function brief(rows: ViewRow[]): string[] {
  return rows.map((r) => {
    if (r.kind === 'gap') return `gap ${r.gap.place} ${r.gap.from}+${r.gap.count ?? '?'}`
    if (r.kind === 'hunk') return r.text
    return `${r.kind} ${r.old ?? ''}/${r.new ?? ''}`
  })
}

const gapsOf = (rows: ViewRow[]) => rows.filter((r): r is Extract<ViewRow, { kind: 'gap' }> => r.kind === 'gap').map((r) => r.gap)

describe('hidden lines of a patch', () => {
  test('a gap above the first change, between the changes and below the last, with their counts', () => {
    const base = hunksOf(100, [10, 90])
    const rows = viewRows(base, null, NOTHING_REVEALED)
    expect(gapsOf(rows)).toEqual<Gap[]>([
      { place: 'top', from: 1, count: 6, before: 0, after: 7 },
      { place: 'between', from: 14, count: 73, before: 13, after: 87 },
      // Three lines follow the last change: the file may go on.
      { place: 'bottom', from: 94, count: null, before: 93, after: null },
    ])
    // The gap rows carry the hunk's own header; every line of the patch shows once.
    expect(rows.filter((r) => r.kind === 'gap').map((r) => r.text)).toEqual(['@@ -7,7 +7,7 @@ fn()', '@@ -87,7 +87,7 @@ fn()', ''])
    expect(rows.filter((r) => r.kind !== 'gap' && r.kind !== 'hunk')).toHaveLength(16)
  })

  test('a change on the first line has no gap above; one near the end shows where the file ends', () => {
    const rows = viewRows(hunksOf(20, [1, 19]), null, NOTHING_REVEALED)
    expect(brief(rows)[0]).toBe('@@ -1,4 +1,4 @@ fn()')
    // Only one line follows line 19: the file ends at 20, nothing below.
    expect(gapsOf(rows).map((g) => g.place)).toEqual(['between'])
    expect(knownEnd(hunksOf(20, [1, 19]), BASE_CONTEXT)).toBe(20)
    expect(knownEnd(hunksOf(100, [10]), BASE_CONTEXT)).toBeNull()
    expect(knownEnd(hunksOf(100, [10], 'all'), 'all')).toBe(100)
    expect(knownEnd(hunksOf(100, [10], 30), 30)).toBeNull()
    expect(knownEnd(hunksOf(100, [80], 30), 30)).toBe(100)
  })

  test('the end of a file without a final line break is known', () => {
    const p = parsePatch('@@ -8,3 +8,3 @@\n a\n-b\n+c\n\\ No newline at end of file\n')
    expect(knownEnd(p.hunks, BASE_CONTEXT)).toBe(9)
    expect(gapsOf(viewRows(p.hunks, null, NOTHING_REVEALED)).map((g) => g.place)).toEqual(['top'])
  })

  test('revealed lines from a wider read show as context with both numbers, and the gaps shrink', () => {
    const base = hunksOf(100, [10, 90])
    const between = gapsOf(viewRows(base, null, NOTHING_REVEALED))[1]!
    const reveal = revealFor(NOTHING_REVEALED, { kind: 'below', gap: between })
    expect(reveal.ranges).toEqual([{ from: 14, to: 33 }])
    const ctx = contextFor(base, reveal)
    expect(ctx).toBe(23)
    const rows = viewRows(base, wider(100, [10, 90], ctx), reveal)
    // 13 shows, then 14..33, then a gap of 34..86 before the second hunk.
    expect(gapsOf(rows)).toEqual<Gap[]>([
      { place: 'top', from: 1, count: 6, before: 0, after: 7 },
      { place: 'between', from: 34, count: 53, before: 33, after: 87 },
      // The wider read reaches the file's end: the lines below are counted now.
      { place: 'bottom', from: 94, count: 7, before: 93, after: null },
    ])
    const lines = rows.filter((r): r is LineRow => r.kind === 'ctx')
    expect(lines.find((r) => r.new === 33)).toEqual({ kind: 'ctx', text: 'line 33', old: 33, new: 33 })
    // The first segment grew, so its header is written anew; the words after it stay.
    expect(rows.find((r) => r.kind === 'gap')!.text).toBe('@@ -7,27 +7,27 @@ fn()')

    // Above the second hunk, then the rest of the gap: no gap is left between them.
    const r2 = revealFor(reveal, { kind: 'above', gap: gapsOf(rows)[1]! })
    expect(r2.ranges[1]).toEqual({ from: 67, to: 86 })
    const r3 = revealFor(r2, { kind: 'gap', gap: gapsOf(viewRows(base, wider(100, [10, 90], contextFor(base, r2)), r2))[1]! })
    const all = viewRows(base, wider(100, [10, 90], contextFor(base, r3)), r3)
    expect(gapsOf(all).map((g) => g.place)).toEqual(['top', 'bottom'])
  })

  test('the top gap and the rest of the file below', () => {
    const base = hunksOf(100, [30, 60])
    const top = gapsOf(viewRows(base, null, NOTHING_REVEALED))[0]!
    expect(top).toEqual({ place: 'top', from: 1, count: 26, before: 0, after: 27 })
    const above = revealFor(NOTHING_REVEALED, { kind: 'above', gap: top })
    expect(above.ranges).toEqual([{ from: 7, to: 26 }])
    const rows = viewRows(base, wider(100, [30, 60], contextFor(base, above)), above)
    expect(gapsOf(rows)[0]).toEqual({ place: 'top', from: 1, count: 6, before: 0, after: 7 })

    const bottom = gapsOf(rows).at(-1)!
    const rest = revealFor(above, { kind: 'gap', gap: bottom })
    expect(rest.ranges.at(-1)).toEqual({ from: 64, to: Infinity })
    expect(contextFor(base, rest)).toBe('all')
    const done = viewRows(base, wider(100, [30, 60], 'all'), rest)
    expect(gapsOf(done).map((g) => g.place)).toEqual(['top', 'between'])
    expect(done.filter((r) => r.kind === 'ctx').at(-1)).toEqual({ kind: 'ctx', text: 'line 100', old: 100, new: 100 })
    // Below the last line: once the end is known, a short rest says how many lines.
    const twenty = revealFor(NOTHING_REVEALED, { kind: 'below', gap: gapsOf(viewRows(base, null, NOTHING_REVEALED)).at(-1)! })
    const shorter = viewRows(base, wider(100, [30, 60], contextFor(base, twenty)), twenty)
    expect(gapsOf(shorter).at(-1)).toEqual({ place: 'bottom', from: 84, count: null, before: 83, after: null })
  })

  test('the whole file shows every line and no expander', () => {
    const base = hunksOf(50, [25])
    const reveal = revealFor(NOTHING_REVEALED, { kind: 'all' })
    expect(contextFor(base, reveal)).toBe('all')
    const rows = viewRows(base, wider(50, [25], 'all'), reveal)
    expect(gapsOf(rows)).toEqual([])
    expect(brief(rows)[0]).toBe('@@ -1,50 +1,50 @@ fn()')
    expect(rows.filter((r) => r.kind === 'ctx')).toHaveLength(49)
  })

  test('a wider read hides what was not asked for: the patch looks the same until lines are revealed', () => {
    const base = hunksOf(100, [10, 90])
    expect(brief(viewRows(base, wider(100, [10, 90], 'all'), NOTHING_REVEALED))).toEqual(brief(viewRows(base, null, NOTHING_REVEALED)).map((r) => (r.startsWith('gap bottom') ? 'gap bottom 94+7' : r)))
  })

  test('a lost line break at the end of the old side keeps its note next to its line', () => {
    const p = parsePatch('@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n')
    expect(brief(viewRows(p.hunks, null, NOTHING_REVEALED))).toEqual(['@@ -1,2 +1,2 @@', 'ctx 1/1', 'del 2/', 'note /', 'add /2'])
  })

  test('the context to ask for, and whether a read holds it', () => {
    const base = hunksOf(1000, [500])
    expect(contextFor(base, NOTHING_REVEALED)).toBe(BASE_CONTEXT)
    expect(contextFor(base, { ranges: [{ from: 100, to: 120 }], all: false })).toBe(497 - 100 + BASE_CONTEXT)
    expect(contextFor(base, { ranges: [{ from: 1, to: 2_000_000 }], all: false })).toBe('all')
    // The middle of a gap is farther from both hunks than its ends.
    const two = hunksOf(1000, [100, 900])
    expect(contextFor(two, { ranges: [{ from: 104, to: 896 }], all: false })).toBe(500 - 103 + BASE_CONTEXT)
    expect(covers(null, 3)).toBe(true)
    expect(covers(null, 4)).toBe(false)
    expect(covers(30, 20)).toBe(true)
    expect(covers(30, 'all')).toBe(false)
    expect(covers('all', 9999)).toBe(true)
  })

  test('a wider read of a file that changed meanwhile is not the same', () => {
    expect(sameChanges(hunksOf(100, [10]), hunksOf(100, [10], 'all'))).toBe(true)
    expect(sameChanges(hunksOf(100, [10]), hunksOf(100, [11], 'all'))).toBe(false)
  })

  test('which files offer hidden lines', () => {
    const p = parsePatch(patchOf(10, [5], 3))
    const f = { binary: false, status: 'modified', patch: 'x', truncated: false }
    expect(expandable(f, p)).toBe(true)
    expect(expandable({ ...f, status: 'renamed' }, p)).toBe(true)
    for (const o of [{ binary: true }, { status: 'added' }, { status: 'deleted' }, { status: 'untracked' }, { truncated: true }, { patch: null }]) expect(expandable({ ...f, ...o }, p)).toBe(false)
    expect(expandable(f, parsePatch('@@ -1,3 +1,3 @@\n a\n'))).toBe(false)
  })
})

describe('expander buttons', () => {
  const gap = (o: Partial<Gap>): Gap => ({ place: 'between', from: 100, count: 57, before: 99, after: 157, ...o })
  const names = (g: Gap) => expanders(g, 'src/f.ts').map((b) => b.name)

  test('a long gap between changes: twenty below, twenty above, all of it', () => {
    expect(names(gap({}))).toEqual([
      'Show 20 hidden lines below line 99 of src/f.ts',
      'Show 20 hidden lines above line 157 of src/f.ts',
      'Show all 57 hidden lines between lines 99 and 157 of src/f.ts',
    ])
    expect(expanders(gap({}), 'src/f.ts')[0]!.text).toBe('Show 20 hidden lines below line 99')
  })

  test('a short gap is one button', () => {
    expect(names(gap({ count: 1, from: 100, after: 101 }))).toEqual(['Show 1 hidden line between lines 99 and 101 of src/f.ts'])
    expect(names(gap({ place: 'top', before: 0, from: 1, count: 6, after: 7 }))).toEqual(['Show 6 hidden lines above line 7 of src/f.ts'])
  })

  test('the top and the bottom, known and unknown', () => {
    expect(names(gap({ place: 'top', before: 0, from: 1, count: 1199, after: 1200 }))).toEqual([
      'Show 20 hidden lines above line 1,200 of src/f.ts',
      'Show all 1,199 hidden lines above line 1,200 of src/f.ts',
    ])
    expect(names(gap({ place: 'bottom', count: null, after: null }))).toEqual(['Show 20 more lines below line 99 of src/f.ts', 'Show the rest of the file below line 99 of src/f.ts'])
    expect(names(gap({ place: 'bottom', count: 30, after: null }))).toEqual(['Show 20 hidden lines below line 99 of src/f.ts', 'Show all 30 hidden lines below line 99 of src/f.ts'])
    expect(names(gap({ place: 'bottom', count: 3, after: null }))).toEqual(['Show 3 hidden lines below line 99 of src/f.ts'])
  })
})
