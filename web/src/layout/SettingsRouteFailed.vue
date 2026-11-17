<script setup lang="ts">
// Shown in place of the settings when their code cannot be loaded: the
// message stands where the settings' page would, in the settings' frame.
import { ChunkFailed, FAILED_PAGE_TITLE } from '../components/ui/chunkFailure'
import './settingsFrame.css'
</script>

<template>
  <section class="settings-frame settings-route-failed" data-route-failed>
    <h1 class="sr-only">{{ FAILED_PAGE_TITLE }}</h1>
    <ChunkFailed />
  </section>
</template>

<style scoped>
/* The message is in the page's column: the list's width and the gap are left empty. */
.settings-route-failed {
  padding-top: calc(var(--omo-space) * 2.5);
  padding-right: var(--settings-edge);
}
.settings-route-failed > :not(h1) {
  margin-left: calc(var(--settings-list) + var(--settings-gap));
  max-width: var(--settings-page-max);
}
</style>
