// Hidden lines of a file's diff and how to show them, as on a pull request:
// a patch shows each change with three lines around it, the lines between
// (and above the first and below the last change) are hidden behind an
// expander row. Showing them needs the file's patch read again with more
// context (or whole); this file decides which lines show and which rows the
// view draws. Everything is in line numbers of the file's new side: the
// hidden lines are unchanged, so their old numbers follow from the lines
// around them, and the wider patch carries both numbers anyway.
import type { DiffContext } from '../../api/types'
import { MAX_DIFF_CONTEXT } from '../../api/types'
import type { Hunk, ParsedPatch, PatchLine } from './parsePatch'

/** The context the server puts around each change when nothing else is asked. */
export const BASE_CONTEXT = 3
/** Lines one "Show 20 …" adds. */
export const EXPAND_STEP = 20

/** A run of new-side line numbers, `to` included; `to` may be Infinity (to the file's end). */
export interface LineRange {
  from: number
  to: number
}

/** What the user asked to see of one file beyond its patch. */
export interface Reveal {
  ranges: LineRange[]
  /** The whole file. */
  all: boolean
}

export const NOTHING_REVEALED: Reveal = { ranges: [], all: false }

/** A wider patch of the file as the server sent it. */
export interface Wider {
  parsed: ParsedPatch
  context: DiffContext
}

export type GapPlace = 'top' | 'between' | 'bottom'

/** One expander row: the hidden lines between two shown ones. */
export interface Gap {
  place: GapPlace
  /** The first hidden line (new side). */
  from: number
  /** How many lines are hidden; null when the file's end is not known yet. */
  count: number | null
  /** The last shown line above (new side); 0 at the file's top. */
  before: number
  /** The first shown line below (new side); null at the bottom. */
  after: number | null
}

export type ExpandAction =
  | { kind: 'below'; gap: Gap } // the EXPAND_STEP lines right below `before`
  | { kind: 'above'; gap: Gap } // the EXPAND_STEP lines right above `after`
  | { kind: 'gap'; gap: Gap } // the whole gap
  | { kind: 'all' } // the whole file

/** A line of the patch (or a revealed one) as a row. */
export interface LineRow {
  kind: PatchLine['kind']
  text: string
  old: number | null
  new: number | null
}

export type ViewRow = { kind: 'hunk'; text: string } | { kind: 'gap'; text: string; gap: Gap } | LineRow

/** The new-side numbers of the context lines a patch shows. */
function contextLines(hunks: Hunk[]): Set<number> {
  const out = new Set<number>()
  for (const h of hunks) for (const l of h.lines) if (l.kind === 'ctx' && l.new !== undefined) out.add(l.new)
  return out
}

/** The new-side lines a hunk covers, as a range (empty for a hunk without new lines: `to` < `from`). */
function newSpan(h: Hunk): LineRange {
  let last = h.newStart - 1
  for (const l of h.lines) if (l.new !== undefined) last = l.new
  return { from: h.newStart, to: last }
}

/** The changed lines of a patch in order, for comparing two reads of one file. */
function changes(hunks: Hunk[]): string {
  const out: string[] = []
  for (const h of hunks) for (const l of h.lines) if (l.kind === 'add' || l.kind === 'del') out.push(`${l.kind}${l.old ?? ''}:${l.new ?? ''}:${l.text}`)
  return out.join('\n')
}

/** Whether a wider read shows the same changes as the patch: when not, the file changed in between. */
export function sameChanges(base: Hunk[], wider: Hunk[]): boolean {
  return changes(base) === changes(wider)
}

/**
 * The last line of the file (new side) when a patch read with `context`
 * lines of context shows it: the whole file was asked for, or fewer context
 * lines than asked follow the last change. null when the file goes on.
 */
export function knownEnd(hunks: Hunk[], context: DiffContext): number | null {
  const last = hunks[hunks.length - 1]
  if (!last) return null
  let lastNew = 0
  for (const h of hunks) {
    const s = newSpan(h)
    if (s.to >= s.from) lastNew = s.to
  }
  if (context === 'all') return lastNew
  let trailing = 0
  for (let i = last.lines.length - 1; i >= 0; i--) {
    const l = last.lines[i]!
    if (l.kind === 'note') return lastNew // "No newline at end of file"
    if (l.kind !== 'ctx') break
    trailing++
  }
  return trailing < context ? lastNew : null
}

/**
 * The context to ask for so that every revealed line is in the patch: the
 * distance of the farthest revealed line from the nearest hunk of the
 * patch, plus the patch's own context (a hunk's edge is at most that far
 * from its change, so this is never too little). The whole file when the
 * reveal reaches the file's end.
 */
export function contextFor(base: Hunk[], reveal: Reveal): DiffContext {
  if (reveal.all) return 'all'
  let need = BASE_CONTEXT
  const spans = base.map(newSpan).sort((x, y) => x.from - y.from)
  const distance = (line: number) => {
    let d = Infinity
    for (const sp of spans) d = Math.min(d, line < sp.from ? sp.from - line : line > sp.to ? line - sp.to : 0)
    return d
  }
  for (const r of reveal.ranges) {
    if (r.to === Infinity) return 'all'
    // The farthest line of a range from every hunk is one of its ends or the middle between two hunks.
    const lines = [r.from, r.to]
    for (let i = 0; i + 1 < spans.length; i++) {
      const mid = Math.floor((spans[i]!.to + spans[i + 1]!.from) / 2)
      for (const m of [mid, mid + 1]) if (m > r.from && m < r.to) lines.push(m)
    }
    for (const line of lines) {
      const d = distance(line)
      if (d !== Infinity) need = Math.max(need, d + BASE_CONTEXT)
    }
  }
  return need >= MAX_DIFF_CONTEXT ? 'all' : need
}

/** Whether a patch read with `have` context holds every line `want` asks for. */
export function covers(have: DiffContext | null, want: DiffContext): boolean {
  if (have === null) return want !== 'all' && want <= BASE_CONTEXT
  if (have === 'all') return true
  return want !== 'all' && want <= have
}

/** The lines an action reveals, added to what is revealed. */
export function revealFor(reveal: Reveal, action: ExpandAction): Reveal {
  if (action.kind === 'all') return { ranges: reveal.ranges, all: true }
  const g = action.gap
  const end = g.count === null ? Infinity : g.from + g.count - 1
  let r: LineRange
  if (action.kind === 'gap') r = { from: g.from, to: end }
  else if (action.kind === 'below') r = { from: g.from, to: Math.min(end, g.from + EXPAND_STEP - 1) }
  else r = { from: Math.max(g.from, (g.after ?? end + 1) - EXPAND_STEP), to: end }
  return { ranges: [...reveal.ranges, r], all: reveal.all }
}

const inRanges = (n: number, ranges: LineRange[]) => ranges.some((r) => n >= r.from && n <= r.to)

/** `-3,4` as git writes it: the count left out when it is 1, the line before for an empty side. */
function side(sign: string, start: number, count: number): string {
  if (count === 0) return `${sign}${start - 1},0`
  return count === 1 ? `${sign}${start}` : `${sign}${start},${count}`
}

interface Shown {
  line: PatchLine
  /** Where the line sits on each side (a removed line sits before the new line of that number). */
  oldAt: number
  newAt: number
}

/**
 * The rows of a file: its shown lines in segments, each segment after a
 * header (`@@ -a,b +c,d @@`, the patch's own where a segment is one of its
 * hunks) or, when lines are hidden before it, after an expander row. A last
 * expander row follows when lines may be hidden below the last segment.
 * `wider` is a read of the file with more context; without it only the
 * patch's own lines show.
 */
export function viewRows(base: Hunk[], wider: Wider | null, reveal: Reveal): ViewRow[] {
  const source = wider?.parsed.hunks ?? base
  const baseCtx = contextLines(base)
  const visible = (l: PatchLine) => l.kind !== 'ctx' || reveal.all || baseCtx.has(l.new!) || inRanges(l.new!, reveal.ranges)

  // The shown lines, with where each sits.
  const shown: Shown[] = []
  for (const h of source) {
    let oldAt = h.oldStart
    let newAt = h.newStart
    let prevShown = false
    for (const l of h.lines) {
      if (l.kind === 'note') {
        if (prevShown) shown.push({ line: l, oldAt, newAt })
        continue
      }
      const at = { oldAt: l.old ?? oldAt, newAt: l.new ?? newAt }
      prevShown = visible(l)
      if (prevShown) shown.push({ line: l, ...at })
      if (l.old !== undefined) oldAt = l.old + 1
      if (l.new !== undefined) newAt = l.new + 1
    }
  }

  // Cut into segments where lines are skipped.
  const segments: Shown[][] = []
  let lastOld = 0
  let lastNew = 0
  for (const s of shown) {
    const skipped = s.line.kind === 'note' ? false : s.line.kind === 'del' ? s.oldAt > lastOld + 1 : s.newAt > lastNew + 1
    if (skipped || segments.length === 0) segments.push([])
    segments[segments.length - 1]!.push(s)
    if (s.line.old !== undefined) lastOld = s.line.old
    if (s.line.new !== undefined) lastNew = s.line.new
    if (s.line.kind === 'del') lastNew = Math.max(lastNew, s.newAt - 1)
    if (s.line.kind === 'add') lastOld = Math.max(lastOld, s.oldAt - 1)
  }

  const rows: ViewRow[] = []
  let prevNew = 0
  for (const seg of segments) {
    const first = seg[0]!
    const newStart = first.newAt
    const oldStart = first.oldAt
    let oldCount = 0
    let newCount = 0
    for (const s of seg) {
      if (s.line.kind === 'ctx' || s.line.kind === 'del') oldCount++
      if (s.line.kind === 'ctx' || s.line.kind === 'add') newCount++
    }
    const own = base.find((h) => h.newStart === newStart && h.oldStart === oldStart && sameCounts(h, oldCount, newCount))
    // The words after the header come from the patch's hunk that starts in this segment.
    const heading = base.find((h) => h.newStart >= newStart && h.newStart < newStart + Math.max(newCount, 1))
    const text = own ? own.header : `@@ ${side('-', oldStart, oldCount)} ${side('+', newStart, newCount)} @@${sectionOf(heading)}`
    const hidden = newStart - 1 - prevNew
    if (hidden > 0) rows.push({ kind: 'gap', text, gap: { place: prevNew === 0 ? 'top' : 'between', from: prevNew + 1, count: hidden, before: prevNew, after: newStart } })
    else rows.push({ kind: 'hunk', text })
    for (const s of seg) rows.push({ kind: s.line.kind, text: s.line.text, old: s.line.old ?? null, new: s.line.new ?? null })
    prevNew = newStart + newCount - 1
  }

  // Below the last shown line.
  const end = knownEnd(source, wider?.context ?? BASE_CONTEXT)
  if (segments.length > 0) {
    const below = end === null ? null : end - prevNew
    if (below === null || below > 0) rows.push({ kind: 'gap', text: '', gap: { place: 'bottom', from: prevNew + 1, count: below, before: prevNew, after: null } })
  }
  return rows
}

function sameCounts(h: Hunk, oldCount: number, newCount: number): boolean {
  let o = 0
  let n = 0
  for (const l of h.lines) {
    if (l.kind === 'ctx' || l.kind === 'del') o++
    if (l.kind === 'ctx' || l.kind === 'add') n++
  }
  return o === oldCount && n === newCount
}

/** The words git puts after a hunk's header (the function the hunk is in), with their space. */
function sectionOf(h: Hunk | undefined): string {
  if (!h) return ''
  const m = /^@@ [^@]* @@(.*)$/.exec(h.header)
  return m?.[1] ?? ''
}

const fmt = (n: number) => n.toLocaleString('en')

export interface ExpanderButton {
  action: ExpandAction
  /** The words on the button. */
  text: string
  /** Its full name, with the file's name. */
  name: string
}

/** The buttons of an expander row, with names that say which lines of which file they show. */
export function expanders(gap: Gap, path: string): ExpanderButton[] {
  const of = ` of ${path}`
  const out: ExpanderButton[] = []
  const add = (action: ExpandAction, text: string) => out.push({ action, text, name: text + of })
  const n = gap.count
  if (gap.place === 'bottom') {
    if (n === null) {
      add({ kind: 'below', gap }, `Show ${EXPAND_STEP} more lines below line ${fmt(gap.before)}`)
      add({ kind: 'gap', gap }, `Show the rest of the file below line ${fmt(gap.before)}`)
    } else if (n <= EXPAND_STEP) add({ kind: 'gap', gap }, `Show ${hiddenLines(n)} below line ${fmt(gap.before)}`)
    else {
      add({ kind: 'below', gap }, `Show ${EXPAND_STEP} hidden lines below line ${fmt(gap.before)}`)
      add({ kind: 'gap', gap }, `Show all ${fmt(n)} hidden lines below line ${fmt(gap.before)}`)
    }
    return out
  }
  const after = fmt(gap.after!)
  if (gap.place === 'top') {
    if (n! <= EXPAND_STEP) add({ kind: 'gap', gap }, `Show ${hiddenLines(n!)} above line ${after}`)
    else {
      add({ kind: 'above', gap }, `Show ${EXPAND_STEP} hidden lines above line ${after}`)
      add({ kind: 'gap', gap }, `Show all ${fmt(n!)} hidden lines above line ${after}`)
    }
    return out
  }
  const before = fmt(gap.before)
  if (n! <= EXPAND_STEP) add({ kind: 'gap', gap }, `Show ${hiddenLines(n!)} between lines ${before} and ${after}`)
  else {
    add({ kind: 'below', gap }, `Show ${EXPAND_STEP} hidden lines below line ${before}`)
    add({ kind: 'above', gap }, `Show ${EXPAND_STEP} hidden lines above line ${after}`)
    add({ kind: 'gap', gap }, `Show all ${fmt(n!)} hidden lines between lines ${before} and ${after}`)
  }
  return out
}

const hiddenLines = (n: number) => (n === 1 ? '1 hidden line' : `${fmt(n)} hidden lines`)

/** A file whose diff can show hidden lines: a complete patch of a text file that is not new, gone or untracked. */
export function expandable(f: { binary: boolean; status: string; patch: string | null; truncated: boolean }, parsed: ParsedPatch | null): boolean {
  if (f.binary || f.patch === null || f.truncated || parsed === null || parsed.cut || parsed.hunks.length === 0) return false
  return f.status !== 'added' && f.status !== 'deleted' && f.status !== 'untracked'
}
