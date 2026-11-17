<script setup lang="ts">
import type { Role, TicketSummary } from '../../../api/types'
import FieldRow from '../FieldRow.vue'
import { SUPERVISOR_LABEL, supervisorOptions, type FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'
import { useGeneral } from './useGeneral'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const { ceoEnabled } = useGeneral()

const name = (r: Role) => SUPERVISOR_LABEL[r] ?? r
const optionLabel = (r: Role) => (r === 'ceo' && !ceoEnabled.value ? 'CEO (switched off)' : name(r))

const edit = useFieldEdit<Role>({
  number: () => props.ticket.number,
  current: () => props.ticket.supervisor,
  patch: (v) => ({ supervisor: v }),
  text: name,
  label: 'Supervisor',
})
</script>

<template>
  <FieldRow
    field="supervisor"
    label="Supervisor"
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
    <template #value>{{ name(edit.shown.value) }}</template>
    <template #edit="{ labelId }">
      <select v-model="edit.draft.value" class="omo-input" :aria-labelledby="labelId">
        <option v-for="r in supervisorOptions(ticket, { ceoEnabled })" :key="r" :value="r" :disabled="r === 'ceo' && !ceoEnabled && ticket.supervisor !== 'ceo'">{{ optionLabel(r) }}</option>
      </select>
    </template>
  </FieldRow>
</template>
