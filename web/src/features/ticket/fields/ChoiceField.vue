<script setup lang="ts">
// A field with a few named values and a default that is shown with the value
// it currently stands for ("Default (Agent)"). Choosing the default sends null.
import { computed } from 'vue'
import type { TicketSummary, UpdateTicket } from '../../../api/types'
import FieldRow from '../FieldRow.vue'
import type { FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'

export interface Choice {
  value: string
  label: string
}

const props = defineProps<{
  ticket: TicketSummary
  state: FieldState
  field: string
  label: string
  /** The request key this field sets. */
  patchKey: 'review' | 'when_approved' | 'task_release'
  /** The ticket's own value; empty means the default. */
  current: string
  /** What the default stands for, in words; empty when not known. */
  defaultWords: string
  choices: Choice[]
}>()

const words = (v: string) => (v === '' ? 'the default' : (props.choices.find((c) => c.value === v)?.label ?? v))

const edit = useFieldEdit<string>({
  number: () => props.ticket.number,
  current: () => props.current,
  patch: (v) => ({ [props.patchKey]: v === '' ? null : v }) as UpdateTicket,
  text: words,
  label: props.label,
})

const defaultLabel = computed(() => (props.defaultWords ? `Default (${props.defaultWords})` : 'Default'))
const valueText = (v: string) => (v === '' ? defaultLabel.value : words(v))
</script>

<template>
  <FieldRow
    :field="field"
    :label="label"
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
    <template #value>
      <span>{{ valueText(edit.shown.value) }}</span>
      <slot name="note" :value="edit.shown.value" />
    </template>
    <template #edit="{ labelId }">
      <select v-model="edit.draft.value" class="omo-input" :aria-labelledby="labelId">
        <option value="">{{ defaultLabel }}</option>
        <option v-for="c in choices" :key="c.value" :value="c.value">{{ c.label }}</option>
      </select>
      <slot name="note" :value="edit.draft.value" />
    </template>
  </FieldRow>
</template>
