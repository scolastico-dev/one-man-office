<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useFieldIds } from './useFieldIds'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    label: string
    /** The server's message: it checks the duration, the field does not. */
    error?: string
    /** What the duration is for; the examples follow it. */
    hint?: string
    id?: string
    /** Go duration texts shown after the hint. */
    examples?: string[]
  }>(),
  { error: undefined, hint: undefined, id: undefined, examples: () => ['45s', '15m', '1h30m'] },
)

/** Go duration text, e.g. `1h30m`, trimmed. */
const model = defineModel<string>({ default: '' })

const text = ref(model.value)

const fullHint = computed(() => {
  const examples = props.examples.length ? `For example ${props.examples.join(', ')}.` : ''
  return [props.hint, examples].filter(Boolean).join(' ')
})

function onInput(event: Event) {
  text.value = (event.target as HTMLInputElement).value
  model.value = text.value.trim()
}

watch(model, (value) => {
  if (text.value.trim() !== value) text.value = value
})

const { id, hintId, errorId, describedBy } = useFieldIds({
  get id() {
    return props.id
  },
  get hint() {
    return fullHint.value || undefined
  },
  get error() {
    return props.error
  },
})
</script>

<template>
  <div class="omo-field duration-field">
    <label :for="id" class="omo-field__label">{{ label }}</label>
    <input
      :id="id"
      v-bind="$attrs"
      :value="text"
      type="text"
      autocomplete="off"
      autocapitalize="off"
      spellcheck="false"
      class="omo-input duration-field__input"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
      @input="onInput"
      @blur="text = model"
    />
    <p v-if="fullHint" :id="hintId" class="omo-field__hint">{{ fullHint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
  </div>
</template>

<style scoped>
.duration-field__input {
  max-width: calc(var(--omo-space) * 36);
}
</style>
