// The diff of #13 for the sample's Diff tab, in the shape the server sends,
// and a small reader that turns a patch into rows for the static view.
import type { DiffFile, TicketDiff } from '../api/types'

/** A right-to-left override inside a file name, to show it escaped. */
const RLO = String.fromCodePoint(0x202e)

const useDragPatch = `@@ -12,9 +12,14 @@ export interface DragOptions {
   /** Called with the column under the pointer, or null outside every column. */
   over(column: Column | null): void
   drop(column: Column, before: number | null): void
+  /** Called when Escape or a lost pointer ends the drag without a drop. */
+  cancel(): void
 }
 
-const THRESHOLD_PX = 4
+/** Movement before a press becomes a drag; below it the press is a click. */
+const THRESHOLD_PX = 6
+/** Scroll speed at the edge of a column, in px per frame. */
+const EDGE_SCROLL_PX = 12
 
 export function useDrag(o: DragOptions) {
   let start: { x: number; y: number; pointer: number } | null = null
@@ -48,6 +53,8 @@ export function useDrag(o: DragOptions) {
     if (start === null || e.pointerId !== start.pointer) return
     const moved = Math.hypot(e.clientX - start.x, e.clientY - start.y)
-    if (moved < THRESHOLD_PX) return
+    if (moved < THRESHOLD_PX && !dragging) return
+    // A line that starts like a header stays content: --- a/this is not a file
+    const column = document.elementFromPoint(e.clientX, e.clientY)?.closest<HTMLElement>('[data-drop-column]')?.dataset.dropColumn ?? null
     dragging = true
-    o.over(null)
+    o.over(column as Column | null)
   }
`

const ghostPatch = `@@ -0,0 +1,9 @@
+<script setup lang="ts">
+// The card under the pointer while it is dragged.
+import TicketCard from './TicketCard.vue'
+import type { CardModel } from './model'
+
+defineProps<{ card: CardModel; x: number; y: number }>()
+</script>
+
+<template><TicketCard class="ghost" :card="card" :style="{ translate: \`\${x}px \${y}px\` }" ghost /></template>
`

const specPatch = `@@ -3,6 +3,6 @@ import { card, column, openBoard } from './support/board'
 import { drag } from './support/drag'
 
-test('a card dragged to Ready is approved', async ({ page, omo }) => {
+test('Planning → Ready For Dev approves the ticket', async ({ page, omo }) => {
   const n = await omo.createTicket({ title: 'drag me' })
   await openBoard(page, omo)
   await drag(page, card(page, n), column(page, 'Ready For Dev'))
`

const uncommittedPatch = `@@ -40,3 +40,4 @@
   cursor: grab;
   touch-action: none;
+  user-select: none;
 }
`

function file(over: Partial<DiffFile> & Pick<DiffFile, 'path' | 'status'>): DiffFile {
  return { old_path: '', binary: false, additions: 0, deletions: 0, patch: null, truncated: false, ...over }
}

export const sampleDiff: TicketDiff = {
  ticket: 13,
  computed_at: '2026-11-15T11:58:00Z',
  projects: [
    {
      project: 'ui',
      base: 'omo/3',
      branch: 'omo/13',
      base_commit: '4be81c2',
      head_commit: '9f03d7a',
      state: 'ok',
      message: '',
      files: [
        file({ path: 'web/src/features/board/useDrag.ts', status: 'modified', additions: 10, deletions: 3, patch: useDragPatch }),
        file({ path: 'web/src/features/board/DragGhost.vue', status: 'added', additions: 9, patch: ghostPatch }),
        file({ path: 'web/e2e/drag.spec.ts', old_path: 'web/e2e/dragging.spec.ts', status: 'renamed', additions: 1, deletions: 1, patch: specPatch }),
        file({ path: 'web/src/assets/drag-cursor.png', status: 'added', binary: true }),
      ],
      files_truncated: false,
      uncommitted: [
        file({ path: 'web/src/features/board/TicketCard.vue', status: 'modified', additions: 1, patch: uncommittedPatch }),
        file({ path: `notes/drag-todo${RLO}dm.txt`, status: 'untracked' }),
      ],
      uncommitted_truncated: false,
    },
  ],
}

export interface PatchRow {
  kind: 'hunk' | 'add' | 'del' | 'same'
  old: number | null
  new: number | null
  text: string
}

/**
 * Rows of a unified patch. Hunks are read by their line counts, so a content
 * line that looks like a header stays content.
 */
export function patchRows(patch: string): PatchRow[] {
  const rows: PatchRow[] = []
  const lines = patch.split('\n')
  let i = 0
  while (i < lines.length) {
    const head = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/.exec(lines[i]!)
    i++
    if (!head) continue
    let oldNo = Number(head[1])
    let newNo = Number(head[3])
    let oldLeft = head[2] === undefined ? 1 : Number(head[2])
    let newLeft = head[4] === undefined ? 1 : Number(head[4])
    rows.push({ kind: 'hunk', old: null, new: null, text: lines[i - 1]! })
    while ((oldLeft > 0 || newLeft > 0) && i < lines.length) {
      const line = lines[i++]!
      const text = line.slice(1)
      if (line.startsWith('+')) {
        rows.push({ kind: 'add', old: null, new: newNo++, text })
        newLeft--
      } else if (line.startsWith('-')) {
        rows.push({ kind: 'del', old: oldNo++, new: null, text })
        oldLeft--
      } else {
        rows.push({ kind: 'same', old: oldNo++, new: newNo++, text })
        oldLeft--
        newLeft--
      }
    }
  }
  return rows
}
