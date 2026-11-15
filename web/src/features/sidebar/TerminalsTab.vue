<script setup lang="ts">
// The Terminals tab: the user's own shells. `selected` is the id of the shell
// to show, when another feature opened the tab on one.
defineProps<{ selected: string | null }>()
</script>

<template>
  <div class="terminals">
    <p class="terminals__note">Your terminals keep running when the browser closes.</p>
  </div>
</template>

<style scoped>
.terminals {
  padding: calc(var(--omo-space) * 3);
}
.terminals__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
