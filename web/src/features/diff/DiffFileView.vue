<script setup lang="ts">
// One file of a diff: a header that opens and closes it (status, name, line
// counts), and its patch as rows with old and new line numbers and a sign.
// Every name and line comes from the agent's work and is shown as text, with
// control and reordering characters escaped. A long patch shows its first
// ROW_STEP rows and more on request. A complete patch of a changed file
// offers its hidden lines (between, above and below the changes, or the
// whole file; see expand.ts): they come as context lines through the same
// lineText as every other line.
import { computed, nextTick, ref, watch } from 'vue'
import type { DiffFile } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { expandable, expanders, NOTHING_REVEALED, viewRows, type ExpandAction, type ExpanderButton, type Gap } from './expand'
import { fileStatus } from './fileStatus'
import { lineText, parsePatch, ROW_STEP } from './parsePatch'
import type { Expansion } from './useDiff'

const props = withDefaults(
  defineProps<{
    file: DiffFile
    /** Id of the file's element, for jumps from the file list. */
    domId: string
    open: boolean
    /** The file is being read on its own. */
    loading?: boolean
    /** The file was read on its own: it shows all the server allows. */
    loaded?: boolean
    /** Why reading the file on its own failed. */
    error?: string
    /** The file's changes are not committed (a file can be in both lists). */
    uncommitted?: boolean
    /** What the file shows beyond its patch. */
    expansion?: Expansion | null
  }>(),
  { loading: false, loaded: false, error: '', uncommitted: false, expansion: null },
)
const emit = defineEmits<{ toggle: [open: boolean]; more: []; expand: [action: ExpandAction] }>()


const path = computed(() => visibleText(props.file.path))
const oldPath = computed(() => visibleText(props.file.old_path))
const status = computed(() => fileStatus(props.file.status))

interface Row {
  kind: 'hunk' | 'gap' | 'add' | 'del' | 'ctx' | 'note'
  text: string
  cut: number
  old: number | null
  new: number | null
  gap?: Gap
  buttons?: ExpanderButton[]
}

// Read only while the file is open: a closed file costs nothing.
const parsed = computed(() => (props.open && props.file.patch !== null ? parsePatch(props.file.patch) : null))
/** How many rows are shown; starts again at ROW_STEP for a new patch. */
const limit = ref(ROW_STEP)
watch(
  () => props.file.patch,
  () => {
    limit.value = ROW_STEP
  },
)
/** The file can show its hidden lines. */
const canExpand = computed(() => expandable(props.file, parsed.value))
/** Every row: hunk headers or expander rows, and lines (revealed ones included). */
const allRows = computed(() => {
  const hunks = parsed.value?.hunks ?? []
  if (canExpand.value) {
    const x = props.expansion
    return viewRows(hunks, x?.wider ?? null, x?.reveal ?? NOTHING_REVEALED)
  }
  return hunks.flatMap((h) => [{ kind: 'hunk' as const, text: h.header }, ...h.lines.map((l) => ({ kind: l.kind, text: l.text, old: l.old ?? null, new: l.new ?? null }))])
})
const total = computed(() => allRows.value.length)
const rows = computed<Row[]>(() =>
  allRows.value.slice(0, limit.value).map((r) => {
    if (r.kind === 'hunk') return { kind: 'hunk', ...lineText(r.text), old: null, new: null }
    if (r.kind === 'gap') return { kind: 'gap', ...lineText(r.text), old: null, new: null, gap: r.gap, buttons: expanders(r.gap, path.value) }
    return { kind: r.kind, ...lineText(r.text), old: r.old, new: r.new }
  }),
)
/** Lines are hidden somewhere: the whole file is offered. */
const hiding = computed(() => canExpand.value && allRows.value.some((r) => r.kind === 'gap'))
const expanding = computed(() => props.expansion?.loading === true)
const errorAt = (at: number) => (props.expansion && !props.expansion.loading && props.expansion.error !== '' && props.expansion.errorAt === at ? props.expansion.error : '')

/** Asks for hidden lines; afterwards the same button of the row that stays in that place takes the focus (else the changes do). */
async function expand(b: ExpanderButton, e: Event) {
  const hadFocus = e.currentTarget instanceof HTMLElement && e.currentTarget === document.activeElement
  emit('expand', b.action)
  if (!hadFocus || b.action.kind === 'all') return
  const g = b.action.gap
  // The read (when one is needed) and the rows that follow it.
  await nextTick()
  if (props.expansion?.loading) {
    await new Promise<void>((resolve) => {
      const stop = watch(
        () => props.expansion?.loading,
        (loading) => {
          if (loading) return
          stop()
          resolve()
        },
      )
    })
  }
  await nextTick()
  const root = region.value
  if (!root) return
  // Lines shown below a row's top keep its lower end, lines shown above it keep its upper end.
  const row = b.action.kind === 'below' ? `[data-gap-after="${g.after ?? 'end'}"]` : `[data-gap-before="${g.before}"]`
  const same = root.querySelector<HTMLElement>(`${row} [data-expand="${b.action.kind}"]`) ?? root.querySelector<HTMLElement>(`${row} [data-expand]`)
  ;(same ?? root).focus()
}
const rowsLeft = computed(() => total.value - rows.value.length)
const fmt = (n: number) => n.toLocaleString('en')
const region = ref<HTMLElement>()

/** Shows the next ROW_STEP rows; after the last step the button is gone and the changes take the focus. */
async function showMoreRows() {
  limit.value += ROW_STEP
  if (rowsLeft.value > 0) return
  await nextTick()
  region.value?.focus()
}

/** The patch is shown, but not all of it. */
const partial = computed(() => props.file.patch !== null && (props.file.truncated || parsed.value?.cut === true))
const leftOut = computed(() => props.file.patch === null && props.file.truncated && !props.file.binary)
const bodyId = computed(() => `${props.domId}-body`)
</script>

<template>
  <li :id="domId" class="dfile" data-diff-file>
    <button
      type="button"
      class="dfile__head"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="bodyId"
      data-file-toggle
      @click="emit('toggle', !open)"
    >
      <span class="dfile__caret" aria-hidden="true">{{ open ? '▾' : '▸' }}</span>
      <span class="dfile__status" :class="`dfile__status--${file.status}`" data-status :title="status[1]"
        ><span aria-hidden="true">{{ status[0] }}</span><span class="sr-only">{{ status[1] }}</span></span
      >
      <span class="dfile__path"
        ><span data-path>{{ path }}</span
        ><span v-if="file.old_path" class="dfile__old"> renamed from {{ oldPath }}</span></span
      >
      <span v-if="file.binary" class="dfile__note">binary</span>
      <span v-else-if="file.status === 'untracked'" class="dfile__note">contents not shown</span>
      <span v-else class="dfile__counts"
        ><span class="dfile__add">+{{ file.additions }}</span> <span class="dfile__del">−{{ file.deletions }}</span></span
      >
    </button>
    <div v-if="open" :id="bodyId" class="dfile__inner">
      <p v-if="file.binary" class="dfile__msg" data-note>Binary file, not shown.</p>
      <p v-else-if="file.status === 'untracked'" class="dfile__msg" data-note>Not committed yet and not tracked by git; its contents are not shown.</p>
      <p v-else-if="leftOut" class="dfile__msg" data-note>
        Too large to show here.
        <UiButton size="sm" :disabled="loading" data-more @click="emit('more')">{{ loading ? 'Loading…' : 'Show anyway' }}</UiButton>
      </p>
      <p v-else-if="file.patch === null || rows.length === 0" class="dfile__msg" data-note>No changes in the text.</p>
      <p v-if="hiding || errorAt(0)" class="dfile__msg dfile__tools" data-expand-tools>
        <UiButton v-if="hiding" size="sm" :disabled="expanding" :aria-label="`Show the whole file ${path}`" data-whole-file @click="emit('expand', { kind: 'all' })">
          {{ expanding && expansion?.errorAt === 0 ? 'Loading…' : 'Show the whole file' }}
        </UiButton>
        <span v-if="errorAt(0)" class="dfile__error" role="alert" data-expand-error>{{ errorAt(0) }}</span>
      </p>
      <div v-if="rows.length > 0" ref="region" class="dfile__body" tabindex="0" role="region" :aria-label="`${uncommitted ? 'Uncommitted changes' : 'Changes'} in ${path}`">
        <div class="dfile__rows">
          <div
            v-for="(row, r) in rows"
            :key="row.kind === 'gap' ? `gap-${row.gap!.from}` : r"
            class="drow"
            :class="`drow--${row.kind}`"
            :data-row="row.kind === 'hunk' || row.kind === 'gap' ? null : row.kind"
            :data-gap="row.kind === 'gap' ? row.gap!.place : undefined"
            :data-gap-before="row.kind === 'gap' ? row.gap!.before : undefined"
            :data-gap-after="row.kind === 'gap' ? (row.gap!.after ?? 'end') : undefined"
          >
            <span v-if="row.kind === 'hunk'" class="drow__hunk" data-hunk>{{ row.text }}</span>
            <span v-else-if="row.kind === 'gap'" class="drow__gap">
              <span class="drow__expand">
                <button
                  v-for="b in row.buttons"
                  :key="b.text"
                  type="button"
                  class="drow__expander"
                  :aria-label="b.name"
                  :disabled="expanding"
                  :data-expand="b.action.kind"
                  @click="expand(b, $event)"
                >
                  {{ b.text }}
                </button>
                <span v-if="expanding && expansion?.errorAt === row.gap!.from" class="drow__state" data-expand-loading>Loading…</span>
                <span v-if="errorAt(row.gap!.from)" class="drow__state drow__state--error" role="alert" data-expand-error>{{ errorAt(row.gap!.from) }}</span>
              </span>
              <span v-if="row.text" class="drow__hunk-text" data-hunk>{{ row.text }}</span>
            </span>
            <template v-else>
              <span class="drow__no" data-no>{{ row.old ?? '' }}</span>
              <span class="drow__no" data-no>{{ row.new ?? '' }}</span>
              <span class="drow__sign"
                ><span aria-hidden="true">{{ row.kind === 'add' ? '+' : row.kind === 'del' ? '−' : ' ' }}</span
                ><span v-if="row.kind === 'add' || row.kind === 'del'" class="sr-only">{{ row.kind === 'add' ? 'added' : 'removed' }}</span></span
              >
              <span class="drow__text" data-text>{{ row.text }}</span>
              <span v-if="row.cut > 0" class="drow__cut" data-cut>… {{ row.cut.toLocaleString('en') }} more characters not shown</span>
            </template>
          </div>
        </div>
      </div>
      <p v-if="rowsLeft > 0" class="dfile__msg" data-rows-left>
        {{ fmt(rowsLeft) }} more lines not shown yet.
        <UiButton size="sm" data-more-rows @click="showMoreRows">Show {{ fmt(Math.min(ROW_STEP, rowsLeft)) }} more lines</UiButton>
      </p>
      <p v-if="partial" class="dfile__msg" data-note>
        Shown in part: the rest of this file's changes is too large to show here.
        <UiButton v-if="!loaded" size="sm" :disabled="loading" data-more @click="emit('more')">{{ loading ? 'Loading…' : 'Show more' }}</UiButton>
      </p>
      <p v-if="error" class="dfile__msg dfile__msg--error" role="alert">{{ error }}</p>
    </div>
  </li>
</template>

<style scoped>
.dfile {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
}
.dfile__head {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  width: 100%;
  min-width: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2.5);
  border: 0;
  background: var(--omo-raised);
  color: var(--omo-fg);
  font: inherit;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: left;
  cursor: pointer;
}
.dfile__head:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.dfile__caret {
  flex-shrink: 0;
  color: var(--omo-muted);
}
.dfile__status {
  flex-shrink: 0;
  width: 2ch;
  color: var(--omo-muted);
  font-weight: 700;
}
.dfile__status--added,
.dfile__status--untracked {
  color: var(--omo-ok);
}
.dfile__status--deleted {
  color: var(--omo-danger);
}
.dfile__status--modified,
.dfile__status--renamed {
  color: var(--omo-accent);
}
.dfile__path {
  flex: 1 1 auto;
  min-width: 0;
  overflow-wrap: anywhere;
}
.dfile__old {
  color: var(--omo-muted);
}
.dfile__counts,
.dfile__note {
  flex-shrink: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dfile__note {
  color: var(--omo-muted);
}
.dfile__add {
  color: var(--omo-ok);
}
.dfile__del {
  color: var(--omo-danger);
}
.dfile__inner {
  border-top: var(--omo-border) solid var(--omo-line);
}
.dfile__msg {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.dfile__body + .dfile__msg {
  border-top: var(--omo-border) solid var(--omo-line);
}
.dfile__msg--error {
  color: var(--omo-danger);
}
.dfile__body {
  /* The expander rows measure the visible width (cqi), not the longest line. */
  container-type: inline-size;
  overflow-x: auto;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.dfile__body:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
/* As wide as the longest line, so every row's fill reaches the end when the box scrolls. */
.dfile__rows {
  width: max-content;
  min-width: 100%;
}
.drow {
  display: grid;
  grid-template-columns: 5ch 5ch 2ch max-content max-content;
  white-space: pre;
}
.drow--add {
  background: var(--omo-diff-add);
}
.drow--del {
  background: var(--omo-diff-del);
}
.drow--hunk,
.drow--gap {
  display: block;
  background: var(--omo-diff-hunk);
  color: var(--omo-muted);
}
/* Stays in view when the lines scroll sideways, and wraps within the visible width. */
.drow__gap {
  position: sticky;
  left: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 calc(var(--omo-space) * 3);
  box-sizing: border-box;
  max-width: 100cqi;
  padding: 0 calc(var(--omo-space) * 2.5);
  white-space: normal;
}
.drow__hunk-text {
  overflow-wrap: anywhere;
}
.drow__expand {
  display: flex;
  flex-wrap: wrap;
  gap: 0 calc(var(--omo-space) * 3);
}
/* A plain link-like control in the row: the row stays one line tall. */
.drow__expander {
  padding: 0;
  border: 0;
  background: none;
  color: var(--omo-accent);
  font: inherit;
  text-align: left;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}
.drow__expander:disabled {
  color: var(--omo-muted);
  cursor: default;
}
.drow__expander:focus-visible {
  outline-offset: 0;
}
.drow__state {
  font-style: italic;
}
.drow__state--error,
.dfile__error {
  color: var(--omo-danger);
  font-style: normal;
  white-space: normal;
}
.drow--note {
  color: var(--omo-muted);
}
.drow__hunk {
  display: block;
  padding: 0 calc(var(--omo-space) * 2.5);
}
.drow__no {
  padding-right: calc(var(--omo-space) * 2);
  color: var(--omo-muted);
  text-align: right;
  user-select: none;
}
.drow__sign {
  user-select: none;
}
.drow--add .drow__sign {
  color: var(--omo-ok);
}
.drow--del .drow__sign {
  color: var(--omo-danger);
}
.drow__text {
  padding-right: calc(var(--omo-space) * 3);
}
.drow__cut {
  padding-right: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-style: italic;
  user-select: none;
}
</style>
