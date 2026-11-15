<script setup lang="ts">
import { useFieldIds } from './useFieldIds'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  label: string
  /** Shown below the field in words and linked with aria-describedby. */
  error?: string
  hint?: string
  id?: string
}>()

const model = defineModel<string>({ default: '' })
const { id, hintId, errorId, describedBy } = useFieldIds(props)
</script>

<template>
  <div class="omo-field">
    <label :for="id" class="omo-field__label">{{ label }}</label>
    <input
      :id="id"
      v-bind="$attrs"
      v-model="model"
      class="omo-input"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
    />
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
  </div>
</template>
