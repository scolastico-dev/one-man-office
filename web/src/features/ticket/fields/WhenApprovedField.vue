<script setup lang="ts">
import { computed } from 'vue'
import type { TicketSummary, WhenApproved } from '../../../api/types'
import { defaultKind, WHEN_LABEL, type FieldState } from '../fieldRules'
import ChoiceField from './ChoiceField.vue'
import { useGeneral } from './useGeneral'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const { general } = useGeneral()

const choices = (Object.keys(WHEN_LABEL) as WhenApproved[]).map((w) => ({ value: w, label: WHEN_LABEL[w] }))
const defaultWords = computed(() => {
  const d: WhenApproved | undefined = general.value?.when_approved_defaults[defaultKind(props.ticket)] ?? (props.ticket.when_approved === null ? props.ticket.effective_when_approved : undefined)
  return d === undefined ? '' : WHEN_LABEL[d]
})
</script>

<template>
  <ChoiceField
    :ticket="ticket"
    :state="state"
    field="when_approved"
    label="When approved"
    patch-key="when_approved"
    :current="ticket.when_approved ?? ''"
    :default-words="defaultWords"
    :choices="choices"
  />
</template>
