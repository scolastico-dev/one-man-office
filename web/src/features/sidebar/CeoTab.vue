<script setup lang="ts">
// The CEO tab. `how` says how the tab became selected: only a `user` choice
// may start the CEO, a tab restored from storage never does.
import type { SelectHow } from './useSidebar'

defineProps<{ how: SelectHow }>()
</script>

<template>
  <div class="ceo">
    <p class="ceo__note">The CEO is not running.</p>
  </div>
</template>

<style scoped>
.ceo {
  padding: calc(var(--omo-space) * 3);
}
.ceo__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
