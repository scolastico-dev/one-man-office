<script setup lang="ts">
defineProps<{ type: 'story' | 'task' }>()
</script>

<template>
  <svg
    class="type-icon"
    :class="`type-icon--${type}`"
    width="12"
    height="12"
    viewBox="0 0 12 12"
    role="img"
    :aria-label="type === 'story' ? 'Story' : 'Task'"
    focusable="false"
  >
    <rect x="0.75" y="0.75" width="10.5" height="10.5" fill="none" stroke="currentColor" stroke-width="1.5" />
    <rect v-if="type === 'story'" x="3.5" y="3.5" width="5" height="5" fill="currentColor" />
  </svg>
</template>

<style scoped>
.type-icon {
  display: inline-block;
  flex-shrink: 0;
  vertical-align: middle;
}
.type-icon--story {
  color: var(--omo-accent);
}
.type-icon--task {
  color: var(--omo-muted);
}
</style>
