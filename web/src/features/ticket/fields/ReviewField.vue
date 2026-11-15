<script setup lang="ts">
import { computed } from 'vue'
import type { Review, TicketSummary } from '../../../api/types'
import { defaultKind, FULL_SUITE_NOTE, lacksFullSuite, REVIEW_LABEL, type FieldState } from '../fieldRules'
import ChoiceField from './ChoiceField.vue'
import { useGeneral } from './useGeneral'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const { general } = useGeneral()

const choices = (Object.keys(REVIEW_LABEL) as Review[]).map((r) => ({ value: r, label: REVIEW_LABEL[r] }))
/** What the default means: the setting for this type, else what the ticket resolves to while it has no value of its own. */
const defaultWords = computed(() => {
  const d: Review | undefined = general.value?.review_defaults[defaultKind(props.ticket)] ?? (props.ticket.review === null ? props.ticket.effective_review : undefined)
  return d === undefined ? '' : REVIEW_LABEL[d]
})
const effectiveOf = (value: string): Review => (value === '' ? (general.value?.review_defaults[defaultKind(props.ticket)] ?? props.ticket.effective_review) : (value as Review))
</script>

<template>
  <ChoiceField
    :ticket="ticket"
    :state="state"
    field="review"
    label="Review"
    patch-key="review"
    :current="ticket.review ?? ''"
    :default-words="defaultWords"
    :choices="choices"
  >
    <template #note="{ value }">
      <p v-if="lacksFullSuite(ticket, effectiveOf(value))" class="omo-field__hint" data-full-suite-note>{{ FULL_SUITE_NOTE }}</p>
    </template>
  </ChoiceField>
</template>
