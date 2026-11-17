<script setup lang="ts">
// What a ticket's view shows before it has the ticket: a missing or hidden
// ticket, a failed read with Try again, or (after a moment) a loading line.
// The slot `back` holds the way back to the board.
import UiButton from '../../components/ui/UiButton.vue'
import type { TicketView } from './useTicketView'

defineProps<{ view: TicketView }>()
</script>

<template>
  <div v-if="view.loadState.value === 'missing' || view.loadState.value === 'forbidden'" class="tload" data-ticket-missing>
    <p class="tload__text">
      {{ view.loadState.value === 'missing' ? `Ticket #${view.number.value} does not exist.` : `You may not see ticket #${view.number.value}.` }}
    </p>
    <slot name="back" primary />
  </div>
  <div v-else-if="view.loadState.value === 'failed'" class="tload" role="alert">
    <p class="tload__text">{{ view.loadError.value }}</p>
    <div class="tload__row">
      <UiButton variant="primary" @click="view.load()">Try again</UiButton>
      <slot name="back" :primary="false" />
    </div>
  </div>
  <p v-else class="tload__text tload__muted" aria-live="polite">{{ view.slow.value ? `Loading #${view.number.value}…` : '' }}</p>
</template>

<style scoped>
.tload {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 4);
}
.tload__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.tload__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.tload__muted {
  color: var(--omo-muted);
}
</style>
