<script setup lang="ts">
import { useFieldIds } from './useFieldIds'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    label: string
    error?: string
    hint?: string
    id?: string
    rows?: number
  }>(),
  { error: undefined, hint: undefined, id: undefined, rows: 6 },
)

const model = defineModel<string>({ default: '' })
const { id, hintId, errorId, describedBy } = useFieldIds(props)
</script>

<template>
  <div class="omo-field">
    <label :for="id" class="omo-field__label">{{ label }}</label>
    <textarea
      :id="id"
      v-bind="$attrs"
      v-model="model"
      :rows="rows"
      class="omo-input resize-y"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
    />
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
  </div>
</template>
