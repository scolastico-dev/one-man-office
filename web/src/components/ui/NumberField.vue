<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useFieldIds } from './useFieldIds'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    label: string
    /** The server's message, shown below the field and linked with aria-describedby. */
    error?: string
    hint?: string
    id?: string
    /** Bounds for the arrow keys and for assistive technology; the server checks the value. */
    min?: number
    max?: number
    /** What one press of an arrow key adds or takes away. */
    step?: number
  }>(),
  { error: undefined, hint: undefined, id: undefined, min: undefined, max: undefined, step: 1 },
)

const model = defineModel<number | null>({ default: null })
/** The parse message while the text is not a whole number, `null` once it is one again. */
const emit = defineEmits<{ parse: [message: string | null] }>()

const PARSE_MESSAGE = 'Enter a whole number'

const text = ref(show(model.value))
const parseError = ref<string | null>(null)

function show(value: number | null): string {
  return value === null || !Number.isFinite(value) ? '' : String(value)
}

/** The number in the text, `null` for no text, `undefined` for text that is not a whole number. */
function parse(raw: string): number | null | undefined {
  const t = raw.trim()
  if (t === '') return null
  return /^[-+]?\d+$/.test(t) ? Number(t) : undefined
}

function setParseError(message: string | null) {
  if (parseError.value === message) return
  parseError.value = message
  emit('parse', message)
}

function onInput(event: Event) {
  text.value = (event.target as HTMLInputElement).value
  const n = parse(text.value)
  if (n === undefined) {
    setParseError(PARSE_MESSAGE)
    return
  }
  setParseError(null)
  model.value = n
}

function clamp(n: number): number {
  if (props.min !== undefined && n < props.min) return props.min
  if (props.max !== undefined && n > props.max) return props.max
  return n
}

function set(n: number) {
  model.value = n
  text.value = show(n)
  setParseError(null)
}

function onKeydown(event: KeyboardEvent) {
  const current = parse(text.value) ?? model.value ?? props.min ?? 0
  let next: number | undefined
  if (event.key === 'ArrowUp') next = current + props.step
  else if (event.key === 'ArrowDown') next = current - props.step
  else if (event.key === 'PageUp') next = current + props.step * 10
  else if (event.key === 'PageDown') next = current - props.step * 10
  else if (event.key === 'Home' && props.min !== undefined) next = props.min
  else if (event.key === 'End' && props.max !== undefined) next = props.max
  if (next === undefined) return
  event.preventDefault()
  set(clamp(next))
}

// A value from outside (Discard, "Use the new value") replaces the text, also text that did not parse.
watch(model, (value) => {
  if (parse(text.value) === value) return
  text.value = show(value)
  setParseError(null)
})

const shownError = computed(() => parseError.value ?? props.error)
const { id, hintId, errorId, describedBy } = useFieldIds({
  get id() {
    return props.id
  },
  get hint() {
    return props.hint
  },
  get error() {
    return shownError.value ?? undefined
  },
})
const valueNow = computed(() => {
  const n = parse(text.value)
  return typeof n === 'number' ? n : undefined
})
</script>

<template>
  <div class="omo-field number-field">
    <label :for="id" class="omo-field__label">{{ label }}</label>
    <input
      :id="id"
      v-bind="$attrs"
      :value="text"
      type="text"
      role="spinbutton"
      inputmode="numeric"
      autocomplete="off"
      spellcheck="false"
      class="omo-input number-field__input"
      :aria-valuenow="valueNow"
      :aria-valuemin="min"
      :aria-valuemax="max"
      :aria-invalid="shownError ? 'true' : undefined"
      :aria-describedby="describedBy"
      @input="onInput"
      @keydown="onKeydown"
    />
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="shownError" :id="errorId" class="omo-field__error">{{ shownError }}</p>
  </div>
</template>

<style scoped>
/* A number is short: the input keeps a width that fits a few digits, the texts below use the full row. */
.number-field__input {
  max-width: calc(var(--omo-space) * 36);
}
</style>
