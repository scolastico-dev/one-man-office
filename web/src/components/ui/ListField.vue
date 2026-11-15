<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import IconButton from './IconButton.vue'
import UiButton from './UiButton.vue'
import { useAnnouncer } from './useAnnouncer'

const props = withDefaults(
  defineProps<{
    /** Names the whole list, e.g. "Base arguments". */
    label: string
    /** One row, capitalised, e.g. "Argument": rows are named "Argument 2", buttons "Move argument 2 up". */
    itemLabel: string
    /** The add button, e.g. "Add argument". */
    addLabel: string
    /** Shown instead of the rows when the list is empty. */
    emptyText?: string
    hint?: string
    /** A message about the whole list. */
    error?: string
    /** Messages per row, by index (the server's `field` names the index). */
    errors?: Record<number, string>
    /** Id of the group. */
    id?: string
    /** Placeholder of every row's input. */
    placeholder?: string
  }>(),
  { emptyText: undefined, hint: undefined, error: undefined, errors: () => ({}), id: undefined, placeholder: undefined },
)

const model = defineModel<string[]>({ default: () => [] })
const announcer = useAnnouncer()

const base = useId()
const groupId = computed(() => props.id ?? `${base}-group`)
const labelId = `${base}-label`
const hintId = `${base}-hint`
const errorId = `${base}-error`
const rowErrorId = (i: number) => `${base}-row-${i}-error`
const describedBy = computed(() => [props.hint ? hintId : '', props.error ? errorId : ''].filter(Boolean).join(' ') || undefined)

/** In names of buttons the row word continues a sentence: "Move argument 2 up". */
const word = computed(() => (/^[A-Z][a-z]/.test(props.itemLabel) ? props.itemLabel[0]!.toLowerCase() + props.itemLabel.slice(1) : props.itemLabel))

// Stable keys per row, so an input keeps its element (and focus) while rows move.
let nextKey = 0
const keys = ref<number[]>(model.value.map(() => nextKey++))
watch(model, (rows) => {
  if (rows.length !== keys.value.length) keys.value = rows.map(() => nextKey++)
})

const root = ref<HTMLElement>()
function focus(selector: string): boolean {
  const el = root.value?.querySelector<HTMLElement>(selector)
  if (!el || (el as HTMLButtonElement).disabled) return false
  el.focus()
  return true
}
const at = (i: number, part: string) => `[data-row="${i}"] [data-part="${part}"]`

function setRow(i: number, value: string) {
  const rows = [...model.value]
  rows[i] = value
  model.value = rows
}

function move(i: number, to: number) {
  const rows = [...model.value]
  const ks = [...keys.value]
  ;[rows[i], rows[to]] = [rows[to]!, rows[i]!]
  ;[ks[i], ks[to]] = [ks[to]!, ks[i]!]
  keys.value = ks
  model.value = rows
  announcer.polite(`${props.itemLabel} moved to place ${to + 1}.`)
  const same = to < i ? 'up' : 'down'
  const other = to < i ? 'down' : 'up'
  void nextTick(() => focus(at(to, same)) || focus(at(to, other)))
}

function remove(i: number) {
  const rows = [...model.value]
  rows.splice(i, 1)
  const ks = [...keys.value]
  ks.splice(i, 1)
  keys.value = ks
  model.value = rows
  announcer.polite(`${props.itemLabel} ${i + 1} removed.`)
  void nextTick(() => focus(at(i, 'input')) || focus(at(i - 1, 'input')) || focus('[data-part="add"]'))
}

function add() {
  keys.value = [...keys.value, nextKey++]
  model.value = [...model.value, '']
  void nextTick(() => focus(at(model.value.length - 1, 'input')))
}
</script>

<template>
  <div ref="root" class="omo-field list-field">
    <p :id="labelId" class="omo-field__label list-field__label">{{ label }}</p>
    <div :id="groupId" role="group" :aria-labelledby="labelId" :aria-describedby="describedBy" class="list-field__group">
      <ul v-if="model.length" class="list-field__rows">
        <li v-for="(value, i) in model" :key="keys[i]" class="list-field__row" :data-row="i">
          <div class="list-field__line">
            <input
              :value="value"
              class="omo-input list-field__input"
              :aria-label="`${itemLabel} ${i + 1}`"
              :aria-invalid="errors[i] ? 'true' : undefined"
              :aria-describedby="errors[i] ? rowErrorId(i) : undefined"
              :placeholder="placeholder"
              autocomplete="off"
              autocapitalize="off"
              spellcheck="false"
              data-part="input"
              @input="setRow(i, ($event.target as HTMLInputElement).value)"
            />
            <IconButton :label="`Move ${word} ${i + 1} up`" :disabled="i === 0" data-part="up" @click="move(i, i - 1)">
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2 8l4-4 4 4" />
              </svg>
            </IconButton>
            <IconButton :label="`Move ${word} ${i + 1} down`" :disabled="i === model.length - 1" data-part="down" @click="move(i, i + 1)">
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2 4l4 4 4-4" />
              </svg>
            </IconButton>
            <IconButton :label="`Remove ${word} ${i + 1}`" data-part="remove" @click="remove(i)">
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
              </svg>
            </IconButton>
          </div>
          <p v-if="errors[i]" :id="rowErrorId(i)" class="omo-field__error">{{ errors[i] }}</p>
        </li>
      </ul>
      <p v-else-if="emptyText" class="list-field__empty">{{ emptyText }}</p>
      <div>
        <UiButton size="sm" data-part="add" @click="add">
          <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <path d="M6 1.5v9M1.5 6h9" stroke="currentColor" stroke-width="1.5" />
          </svg>
          {{ addLabel }}
        </UiButton>
      </div>
    </div>
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
  </div>
</template>

<style scoped>
.list-field__label {
  margin: 0;
}
.list-field__group {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.list-field__rows {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}
.list-field__row {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
.list-field__line {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.list-field__input {
  flex: 1 1 auto;
  margin-right: var(--omo-space);
}
/* A move button that cannot move stays as quiet as the ghost buttons beside it. */
.list-field__line > .omo-btn:disabled {
  border-color: transparent;
  color: var(--omo-line-strong);
}
.list-field__empty {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) dashed var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
