<script setup lang="ts">
import type { TicketSummary } from '../../../api/types'

defineProps<{ ticket: TicketSummary }>()
</script>

<template>
  <h3 class="sr-only">Agents</h3>
</template>
