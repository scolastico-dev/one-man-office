// Reads the unified patch of one file as the server sends it. Hunks are read
// by the line counts in their headers, so a content line that looks like a
// header (`--- a/x`, `diff --git …`, `@@ …`) stays content. Everything outside
// a hunk (the `diff --git`, `index`, `---` and `+++` lines) is skipped.
import { visibleText } from '../../components/ui/visibleText'

export interface PatchLine {
  kind: 'add' | 'del' | 'ctx' | 'note'
  /** Without the sign; for a note, the note's words. */
  text: string
  old?: number
  new?: number
}

export interface Hunk {
  header: string
  oldStart: number
  newStart: number
  lines: PatchLine[]
}

export interface ParsedPatch {
  hunks: Hunk[]
  /** The patch ends inside a hunk: it was cut for size. */
  cut: boolean
}

const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/

export function parsePatch(patch: string): ParsedPatch {
  const hunks: Hunk[] = []
  // A patch that does not end with a line break ends inside a line.
  let cut = patch !== '' && !patch.endsWith('\n')
  const lines = patch.split('\n')
  if (lines[lines.length - 1] === '') lines.pop()

  let i = 0
  while (i < lines.length) {
    const head = HUNK.exec(lines[i]!)
    i++
    if (!head) continue
    let oldNo = Number(head[1])
    let newNo = Number(head[3])
    let oldLeft = head[2] === undefined ? 1 : Number(head[2])
    let newLeft = head[4] === undefined ? 1 : Number(head[4])
    const hunk: Hunk = { header: lines[i - 1]!, oldStart: oldNo, newStart: newNo, lines: [] }
    hunks.push(hunk)
    while ((oldLeft > 0 || newLeft > 0) && i < lines.length) {
      const line = lines[i++]!
      const sign = line[0]
      const text = line.slice(1)
      if (sign === '\\') {
        hunk.lines.push({ kind: 'note', text: text.trim() })
      } else if (sign === '+') {
        hunk.lines.push({ kind: 'add', text, new: newNo++ })
        newLeft--
      } else if (sign === '-') {
        hunk.lines.push({ kind: 'del', text, old: oldNo++ })
        oldLeft--
      } else {
        // A context line; an empty line is an empty context line.
        hunk.lines.push({ kind: 'ctx', text, old: oldNo++, new: newNo++ })
        oldLeft--
        newLeft--
      }
    }
    if (oldLeft > 0 || newLeft > 0) cut = true
    // The note about a missing line break follows the hunk's last line.
    while (i < lines.length && lines[i]!.startsWith('\\')) hunk.lines.push({ kind: 'note', text: lines[i++]!.slice(1).trim() })
  }
  return { hunks, cut }
}

/** Characters of one line shown at most; the rest is left out with a note. */
export const LINE_LIMIT = 2000

/**
 * How many rows (hunk headers and lines) a file shows at first, and how many
 * more each "Show more lines" adds: a patch read in full can have hundreds
 * of thousands of lines.
 */
export const ROW_STEP = 5000

/**
 * A line of the patch as it is shown: at most LINE_LIMIT characters, with
 * every control and reordering character written as an escape. A tab stays
 * a tab: it only moves the text, and code is full of them. `cut` is the
 * number of characters left out.
 */
export function lineText(raw: string): { text: string; cut: number } {
  let kept = raw
  let cut = 0
  if (raw.length > LINE_LIMIT) {
    kept = raw.slice(0, LINE_LIMIT)
    // Never end on the first half of a surrogate pair.
    if (/[\uD800-\uDBFF]$/.test(kept)) kept = kept.slice(0, -1)
    cut = Array.from(raw.slice(kept.length)).length
  }
  return { text: kept.split('\t').map(visibleText).join('\t'), cut }
}
