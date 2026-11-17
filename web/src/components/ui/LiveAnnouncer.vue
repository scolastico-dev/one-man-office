<script setup lang="ts">
import { announcements } from './useAnnouncer'
</script>

<template>
  <div class="sr-only">
    <div aria-live="polite" aria-atomic="true" data-announcer="polite">{{ announcements.polite.value }}</div>
    <div aria-live="assertive" aria-atomic="true" data-announcer="assertive">{{ announcements.assertive.value }}</div>
  </div>
</template>
