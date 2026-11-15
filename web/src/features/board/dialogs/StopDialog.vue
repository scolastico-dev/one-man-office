<script setup lang="ts">
// Confirms stopping a ticket by dragging it from In Progress to Planning or
// Ready For Dev; for a story it lists the running agents of its tasks that
// stop too. Not built yet: renders nothing.
import type { Role, TicketSummary } from '../../../api/types'

defineProps<{
  open: boolean
  ticket: TicketSummary
  to: 'planning' | 'ready'
  /** The running agents that stop with it (a story's tasks included). */
  agents: { ticket: number; role: Role; name: string }[]
}>()
defineEmits<{ close: []; confirm: [] }>()
</script>

<template></template>
