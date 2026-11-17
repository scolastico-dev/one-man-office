<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import Chip from './Chip.vue'
import IconButton from './IconButton.vue'
import UiButton from './UiButton.vue'

const props = withDefaults(
  defineProps<{
    /** Names the table, e.g. "Agent environment". */
    label: string
    /** Variables omo ships; a row with the shipped name and value is marked "shipped default". */
    shipped?: Record<string, string>
    /** Messages by variable name. */
    errors?: Record<string, string>
    /** A message about the variables as a whole (the server's, verbatim), below the table. */
    error?: string
    /** Shown instead of the rows when there is no variable. */
    emptyText?: string
    hint?: string
    /** Id of the group. */
    id?: string
  }>(),
  { shipped: () => ({}), errors: () => ({}), error: undefined, emptyText: undefined, hint: undefined, id: undefined },
)

/** Name to value. Rows without a name are not part of it yet. */
const model = defineModel<Record<string, string>>({ default: () => ({}) })

interface Row {
  key: number
  name: string
  value: string
}

const base = useId()
const groupId = computed(() => props.id ?? `${base}-group`)
const labelId = `${base}-label`
const hintId = `${base}-hint`
const errorId = `${base}-error`
const describedBy = computed(() => [props.hint ? hintId : '', props.error ? errorId : ''].filter(Boolean).join(' ') || undefined)
const rowNoteId = (key: number) => `${base}-row-${key}-note`

let nextKey = 0
const rows = ref<Row[]>([])
let lastSent: Record<string, string> = {}

function sameRecord(a: Record<string, string>, b: Record<string, string>): boolean {
  const ka = Object.keys(a)
  return ka.length === Object.keys(b).length && ka.every((k) => Object.hasOwn(b, k) && b[k] === a[k])
}

/** Rows are sorted by name when a value arrives, never while the user types. */
function load(value: Record<string, string>) {
  rows.value = Object.keys(value)
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({ key: nextKey++, name, value: value[name]! }))
  lastSent = { ...value }
}
load(model.value)
watch(model, (value) => {
  if (!sameRecord(value, lastSent)) load(value)
})

function send() {
  const out: Record<string, string> = {}
  for (const row of rows.value) {
    const name = row.name.trim()
    if (name) out[name] = row.value
  }
  lastSent = out
  model.value = { ...out }
}

const isShipped = (row: Row) => {
  const name = row.name.trim()
  return Object.hasOwn(props.shipped, name) && props.shipped[name] === row.value
}

/** Names on more than one row: only the last one reaches the value. */
const counts = computed(() => {
  const c = new Map<string, number>()
  for (const row of rows.value) {
    const name = row.name.trim()
    if (name) c.set(name, (c.get(name) ?? 0) + 1)
  }
  return c
})
function twiceNote(index: number): string | undefined {
  const row = rows.value[index]!
  const name = row.name.trim()
  const n = counts.value.get(name) ?? 0
  if (n < 2) return undefined
  const later = rows.value.slice(index + 1).some((r) => r.name.trim() === name)
  if (!later) return undefined
  return `${name} is set ${n === 2 ? 'twice' : `${n} times`}; only the last row counts.`
}
const errorOf = (row: Row) => props.errors[row.name.trim()]
function notesOf(index: number): string[] {
  const row = rows.value[index]!
  return [errorOf(row), twiceNote(index)].filter((n): n is string => Boolean(n))
}

const nameLabel = (i: number) => `Name of variable ${i + 1}`
const shownName = (row: Row, i: number) => row.name.trim() || `variable ${i + 1}`

const root = ref<HTMLElement>()
function focus(selector: string): boolean {
  const el = root.value?.querySelector<HTMLElement>(selector)
  if (!el) return false
  el.focus()
  return true
}
const at = (key: number, part: string) => `[data-key="${key}"] [data-part="${part}"]`

function setName(row: Row, name: string) {
  row.name = name
  send()
}
function setValue(row: Row, value: string) {
  row.value = value
  send()
}

function remove(index: number) {
  rows.value.splice(index, 1)
  send()
  const next = rows.value[index] ?? rows.value[index - 1]
  void nextTick(() => (next ? focus(at(next.key, 'name')) : focus('[data-part="add"]')))
}

function add() {
  const row: Row = { key: nextKey++, name: '', value: '' }
  rows.value.push(row)
  void nextTick(() => focus(at(row.key, 'name')))
}
</script>

<template>
  <div ref="root" class="omo-field env" data-env-table>
    <p :id="labelId" class="omo-field__label env__label">{{ label }}</p>
    <div :id="groupId" role="group" :aria-labelledby="labelId" :aria-describedby="describedBy" class="env__group">
      <div v-if="rows.length" class="env__head" aria-hidden="true">
        <span>Name</span>
        <span>Value</span>
      </div>
      <ul v-if="rows.length" class="env__rows">
        <li v-for="(row, i) in rows" :key="row.key" class="env__row" :data-key="row.key" :data-env-row="row.name.trim()">
          <div class="env__name">
            <input
              :value="row.name"
              class="omo-input"
              :aria-label="nameLabel(i)"
              :aria-describedby="notesOf(i).length ? rowNoteId(row.key) : undefined"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              data-part="name"
              @input="setName(row, ($event.target as HTMLInputElement).value)"
            />
            <Chip v-if="isShipped(row)" label="shipped default" class="env__shipped" />
          </div>
          <div class="env__value">
            <input
              :value="row.value"
              class="omo-input"
              :aria-label="`Value of ${shownName(row, i)}`"
              :aria-invalid="errorOf(row) ? 'true' : undefined"
              :aria-describedby="notesOf(i).length ? rowNoteId(row.key) : undefined"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              data-part="value"
              @input="setValue(row, ($event.target as HTMLInputElement).value)"
            />
          </div>
          <IconButton :label="`Remove ${shownName(row, i)}`" class="env__remove" data-part="remove" @click="remove(i)">
            <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
              <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
            </svg>
          </IconButton>
          <div v-if="notesOf(i).length" :id="rowNoteId(row.key)" class="env__notes">
            <p v-for="note in notesOf(i)" :key="note" class="omo-field__error">{{ note }}</p>
          </div>
        </li>
      </ul>
      <p v-else-if="emptyText" class="env__empty">{{ emptyText }}</p>
      <div>
        <UiButton size="sm" data-part="add" @click="add">
          <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <path d="M6 1.5v9M1.5 6h9" stroke="currentColor" stroke-width="1.5" />
          </svg>
          Add variable
        </UiButton>
      </div>
    </div>
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
  </div>
</template>

<style scoped>
/* Name and value side by side; stacked when the table itself is narrow, whatever the window. */
.env {
  container-type: inline-size;
}
.env__label {
  margin: 0;
}
.env__group {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.env__head,
.env__row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 3fr) var(--omo-control-h);
  column-gap: calc(var(--omo-space) * 2);
}
.env__head {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.env__rows {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}
.env__row {
  align-items: start;
  row-gap: var(--omo-space);
}
.env__name {
  display: grid;
  justify-items: start;
  gap: var(--omo-space);
  min-width: 0;
}
.env__value {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.env__notes {
  grid-column: 1 / -1;
  display: grid;
  gap: var(--omo-space);
  margin: 0;
}
.env__notes p {
  margin: 0;
}
.env__empty {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) dashed var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
@container (max-width: 479.98px) {
  .env__head {
    display: none;
  }
  .env__row {
    grid-template-columns: minmax(0, 1fr) var(--omo-control-h);
    padding-bottom: calc(var(--omo-space) * 2);
    border-bottom: var(--omo-border) solid var(--omo-line);
  }
  .env__name {
    grid-column: 1;
    grid-row: 1;
  }
  .env__value {
    grid-column: 1;
    grid-row: 2;
  }
  .env__remove {
    grid-column: 2;
    grid-row: 1;
  }
}
</style>
