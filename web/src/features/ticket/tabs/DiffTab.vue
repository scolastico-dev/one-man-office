<script setup lang="ts">
import type { TicketSummary } from '../../../api/types'

defineProps<{ ticket: TicketSummary }>()
</script>

<template>
  <h3 class="sr-only">Diff</h3>
</template>
