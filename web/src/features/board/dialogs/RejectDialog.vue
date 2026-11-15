<script setup lang="ts">
// Asks for the comment when a review is rejected by dragging it from Review to
// In Progress: title `Reject #n?`, a required comment, button `Reject`.
// Not built yet: renders nothing.
import type { TicketSummary } from '../../../api/types'

defineProps<{ open: boolean; ticket: TicketSummary }>()
defineEmits<{ close: []; confirm: [comment: string] }>()
</script>

<template></template>
