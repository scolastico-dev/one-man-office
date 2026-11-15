<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ state: 'running' | 'starting' | 'waiting' }>()

/** The words assistive technology hears; `waiting` reads as parked. */
const text = computed(() => (props.state === 'waiting' ? 'parked' : props.state))
</script>

<template>
  <span class="dot" :class="`dot--${state}`" :data-state="state">
    <!-- Shape differs per state, so colour is never the only signal. -->
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" focusable="false">
      <circle v-if="state === 'running'" cx="5" cy="5" r="4" fill="currentColor" />
      <template v-else-if="state === 'starting'">
        <circle cx="5" cy="5" r="3.75" fill="none" stroke="currentColor" stroke-width="1.5" />
        <path d="M5 1.25a3.75 3.75 0 0 1 0 7.5z" fill="currentColor" />
      </template>
      <circle v-else cx="5" cy="5" r="3.75" fill="none" stroke="currentColor" stroke-width="1.5" />
    </svg>
    <span class="sr-only">{{ text }}</span>
  </span>
</template>

<style scoped>
.dot {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  vertical-align: middle;
}
.dot--running,
.dot--starting {
  color: var(--omo-ok);
}
.dot--waiting {
  color: var(--omo-muted);
}
@media (prefers-reduced-motion: no-preference) {
  .dot--running svg {
    animation: dot-pulse 2s ease-in-out infinite;
  }
}
@keyframes dot-pulse {
  50% {
    opacity: 0.6;
  }
}
</style>
