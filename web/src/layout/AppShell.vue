<script setup lang="ts">
// The page frame: top bar, an optional sidebar on the left, and the main area.
</script>

<template>
  <div class="shell" :class="{ 'shell--sidebar': $slots.sidebar }">
    <div v-if="$slots.sidebar" class="shell__sidebar"><slot name="sidebar" /></div>
    <header class="shell__top"><slot name="topbar" /></header>
    <main class="shell__main"><slot /></main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: auto minmax(0, 1fr);
  grid-template-areas:
    'top'
    'main';
  height: 100dvh;
  overflow: hidden;
}
.shell--sidebar {
  grid-template-columns: auto minmax(0, 1fr);
  grid-template-areas:
    'side top'
    'side main';
}
.shell__sidebar {
  grid-area: side;
  min-height: 0;
}
/*
 * The sidebar takes the page's two rows as its own (subgrid): its head is
 * exactly as tall as the top bar, so the line below both is one line.
 */
.shell--sidebar > .shell__sidebar {
  display: grid;
  grid-template-rows: subgrid;
}
.shell__top {
  grid-area: top;
  min-width: 0;
}
.shell__main {
  grid-area: main;
  min-width: 0;
  min-height: 0;
  padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 4) calc(var(--omo-space) * 3);
}
@media (max-width: 767.98px) {
  .shell__main {
    padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3) calc(var(--omo-space) * 2);
  }
}
</style>
