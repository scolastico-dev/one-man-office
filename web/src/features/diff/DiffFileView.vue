<script setup lang="ts">
// One file of a diff: a header that opens and closes it (status, name, line
// counts), and its patch as rows with old and new line numbers and a sign.
// Every name and line comes from the agent's work and is shown as text, with
// control and reordering characters escaped. A long patch shows its first
// ROW_STEP rows and more on request.
import { computed, nextTick, ref, watch } from 'vue'
import type { DiffFile } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { fileStatus } from './fileStatus'
import { lineText, parsePatch, ROW_STEP } from './parsePatch'

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
  }>(),
  { loading: false, loaded: false, error: '', uncommitted: false },
)
const emit = defineEmits<{ toggle: [open: boolean]; more: [] }>()


const path = computed(() => visibleText(props.file.path))
const oldPath = computed(() => visibleText(props.file.old_path))
const status = computed(() => fileStatus(props.file.status))

interface Row {
  kind: 'hunk' | 'add' | 'del' | 'ctx' | 'note'
  text: string
  cut: number
  old: number | null
  new: number | null
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
/** All rows of the patch: one per hunk header and line. */
const total = computed(() => (parsed.value?.hunks ?? []).reduce((n, h) => n + 1 + h.lines.length, 0))
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  for (const h of parsed.value?.hunks ?? []) {
    if (out.length >= limit.value) break
    out.push({ kind: 'hunk', ...lineText(h.header), old: null, new: null })
    for (const l of h.lines) {
      if (out.length >= limit.value) break
      out.push({ kind: l.kind, ...lineText(l.text), old: l.old ?? null, new: l.new ?? null })
    }
  }
  return out
})
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
      <div v-if="rows.length > 0" ref="region" class="dfile__body" tabindex="0" role="region" :aria-label="`${uncommitted ? 'Uncommitted changes' : 'Changes'} in ${path}`">
        <div class="dfile__rows">
          <div v-for="(row, r) in rows" :key="r" class="drow" :class="`drow--${row.kind}`" :data-row="row.kind === 'hunk' ? null : row.kind">
            <span v-if="row.kind === 'hunk'" class="drow__hunk" data-hunk>{{ row.text }}</span>
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
.drow--hunk {
  display: block;
  background: var(--omo-diff-hunk);
  color: var(--omo-muted);
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
