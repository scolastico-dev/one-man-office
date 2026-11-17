<script setup lang="ts">
import type { TicketSummary } from '../../../api/types'
import { validParallel } from '../../create/createRules'
import FieldRow from '../FieldRow.vue'
import { SETTING_LABEL, type FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()

const words = (v: string) => (v === '' ? 'no limit' : v)

const edit = useFieldEdit<string>({
  number: () => props.ticket.number,
  current: () => (props.ticket.max_parallel === null ? '' : String(props.ticket.max_parallel)),
  patch: (v) => ({ max_parallel: v.trim() === '' ? null : Number(v.trim()) }),
  text: words,
  label: SETTING_LABEL.maxParallel,
  validate: (v) => (v.trim() === '' || validParallel(v) ? '' : 'Enter a whole number of at least 1, or leave it empty for no limit.'),
})
</script>

<template>
  <FieldRow
    field="max_parallel"
    :label="SETTING_LABEL.maxParallel"
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
    <template #value>{{ edit.shown.value === '' ? 'No limit' : edit.shown.value }}</template>
    <template #edit="{ labelId }">
      <input v-model="edit.draft.value" class="omo-input" inputmode="numeric" placeholder="No limit" :aria-labelledby="labelId" />
      <p class="omo-field__hint">Leave empty for no limit.</p>
    </template>
  </FieldRow>
</template>
