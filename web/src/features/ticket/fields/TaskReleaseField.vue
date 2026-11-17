<script setup lang="ts">
import { computed } from 'vue'
import type { TaskRelease, TicketSummary } from '../../../api/types'
import { RELEASE_LABEL, SETTING_LABEL, type FieldState } from '../fieldRules'
import ChoiceField from './ChoiceField.vue'
import { useGeneral } from './useGeneral'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const { general } = useGeneral()

const choices = (Object.keys(RELEASE_LABEL) as TaskRelease[]).map((r) => ({ value: r, label: RELEASE_LABEL[r] }))
const defaultWords = computed(() => {
  const d: TaskRelease | null = general.value
    ? general.value.tasks.require_user_release
      ? 'user'
      : 'agents'
    : props.ticket.task_release === null
      ? props.ticket.effective_task_release
      : null
  return d === null ? '' : RELEASE_LABEL[d]
})
</script>

<template>
  <ChoiceField
    :ticket="ticket"
    :state="state"
    field="task_release"
    :label="SETTING_LABEL.taskRelease"
    patch-key="task_release"
    :current="ticket.task_release ?? ''"
    :default-words="defaultWords"
    :choices="choices"
  />
</template>
