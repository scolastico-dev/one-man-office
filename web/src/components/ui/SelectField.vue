<script setup lang="ts">
import { useFieldIds } from './useFieldIds'

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  label: string
  options: SelectOption[]
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
    <select
      :id="id"
      v-bind="$attrs"
      v-model="model"
      class="omo-input"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
    >
      <option v-for="option in options" :key="option.value" :value="option.value" :disabled="option.disabled">
        {{ option.label }}
      </option>
    </select>
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
  </div>
</template>
