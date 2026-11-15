<script setup lang="ts">
import type { TicketSummary } from '../../../api/types'
import DiffView from '../../diff/DiffView.vue'

defineProps<{ ticket: TicketSummary }>()
</script>

<template>
  <h3 class="sr-only">Diff</h3>
  <DiffView :ticket="ticket" />
</template>
