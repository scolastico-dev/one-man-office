<script setup lang="ts">
// A line of the thread that says what happened (a move, an agent starting):
// greyed, no box, with its time.
import RelativeTime from '../../components/ui/RelativeTime.vue'

defineProps<{ id: number; text: string; at: string }>()
</script>

<template>
  <p class="status" :data-event="id">
    <span class="status__text">{{ text }}</span> <RelativeTime class="status__time" :at="at" />
  </p>
</template>

<style scoped>
.status {
  margin: 0;
  padding-left: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.status__time {
  white-space: nowrap;
}
</style>
