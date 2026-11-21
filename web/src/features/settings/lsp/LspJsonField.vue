<script setup lang="ts">
import { inject, onBeforeUnmount, ref, useId, watch } from 'vue'
import { FORM_FIELDS } from '../../../components/ui/formFields'
import TextArea from '../../../components/ui/TextArea.vue'
import { sameJSON } from '../useSettingsResource'

defineProps<{ label: string; error?: string; field: string; hint: string }>()
const model = defineModel<Record<string, unknown>>({ required: true })
const form = inject(FORM_FIELDS, null)
// A stable key keeps a blocker attached to this editor while list indices move.
const parseKey = useId()
const text = ref(JSON.stringify(model.value, null, 2))
const parseError = ref<string | null>(null)

function parse(value: string): Record<string, unknown> | null {
  try {
    const parsed: unknown = JSON.parse(value, (_key, v: unknown) => {
      if (typeof v === 'number' && !Number.isFinite(v)) throw new Error('Non-finite number')
      return v
    })
    return typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed) ? parsed as Record<string, unknown> : null
  } catch {
    return null
  }
}
function setError(message: string | null) {
  parseError.value = message
  form?.setParse(parseKey, message)
}
function input(value: string) {
  text.value = value
  const parsed = parse(value)
  setError(parsed === null ? 'Enter a JSON object, such as {"key": "value"}.' : null)
  if (parsed !== null) model.value = parsed
}
function reset() {
  text.value = JSON.stringify(model.value, null, 2)
  setError(null)
}
watch(model, (value) => {
  if (!sameJSON(parse(text.value), value)) reset()
}, { deep: true })
if (form) watch(form.resets, reset)
onBeforeUnmount(() => form?.setParse(parseKey, null))
</script>

<template>
  <TextArea
    :model-value="text"
    :label="label"
    :hint="hint"
    :error="parseError ?? error"
    :data-field="field"
    :rows="5"
    spellcheck="false"
    autocomplete="off"
    autocapitalize="off"
    @update:model-value="input"
  />
</template>
