<script setup lang="ts">
import { computed } from 'vue'
import type { Model, TicketSummary } from '../../../api/types'
import FieldRow from '../FieldRow.vue'
import type { FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'

const props = defineProps<{ ticket: TicketSummary; state: FieldState; models: Model[] }>()

const words = (v: string) => (v === '' ? 'the role\'s default model' : v)

const edit = useFieldEdit<string>({
  number: () => props.ticket.number,
  current: () => props.ticket.model,
  patch: (v) => ({ model: v === '' ? null : v }),
  text: words,
  label: 'Model',
})

/** Models that can be chosen, and the current one even when it can no longer be. */
const options = computed(() => {
  const out = props.models.map((m) => m.id)
  const cur = props.ticket.model
  if (cur !== '' && !out.includes(cur)) out.unshift(cur)
  return out
})
const describe = computed(() => props.models.find((m) => m.id === edit.draft.value)?.description)
const modelOf = (id: string) => props.models.find((m) => m.id === id)
/** Every model is offered; one that agents may not pick says so, and one the server no longer lists is marked. */
const optionLabel = (id: string) => {
  if (props.models.length === 0) return id
  const m = modelOf(id)
  if (m === undefined) return `${id} (not available)`
  return m.selectable ? id : `${id} (not selectable by agents)`
}
</script>

<template>
  <FieldRow
    field="model"
    label="Model"
    :editable="state.editable"
    :locked-reason="state.lockedReason"
    :editing="edit.editing.value"
    :saving="edit.saving.value"
    :error="edit.error.value"
    :changed-note="edit.changedNote.value"
    @edit="edit.start"
    @save="edit.save"
    @cancel="edit.cancel"
    @use-new="edit.useNew"
  >
    <template #value>{{ edit.shown.value === '' ? 'Default (the role\'s model)' : edit.shown.value }}</template>
    <template #edit="{ labelId }">
      <select v-model="edit.draft.value" class="omo-input" :aria-labelledby="labelId">
        <option value="">Default (the role's model)</option>
        <option v-for="id in options" :key="id" :value="id">{{ optionLabel(id) }}</option>
      </select>
      <p v-if="describe" class="omo-field__hint">{{ describe }}</p>
    </template>
  </FieldRow>
</template>
