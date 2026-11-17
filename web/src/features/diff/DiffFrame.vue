<script setup lang="ts">
// Around a ticket's diff, in the panel and on the page: "Reading the diff…",
// a failed first read with Try again, and once read, the bar with the time
// of the diff and Refresh (slot `bar` adds controls at its start), a failed
// later read, and the content (default slot).
import RelativeTime from '../../components/ui/RelativeTime.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import type { DiffController } from './useDiff'

defineProps<{ d: DiffController }>()
</script>

<template>
  <p v-if="d.state.value === 'loading'" class="dframe__note" aria-live="polite">Reading the diff…</p>
  <div v-else-if="d.state.value === 'failed'" class="dframe__note dframe__note--error" role="alert">
    <p class="dframe__line">{{ visibleText(d.error.value) }}</p>
    <UiButton size="sm" @click="d.refresh()">Try again</UiButton>
  </div>
  <template v-else>
    <div class="dframe__bar">
      <slot name="bar" />
      <span class="dframe__end">
        <span v-if="d.diff.value" class="dframe__asof">as of <RelativeTime :at="d.diff.value.computed_at" /></span>
        <UiButton size="sm" :disabled="d.refreshing.value" data-diff-refresh @click="d.refresh()">{{ d.refreshing.value ? 'Refreshing…' : 'Refresh' }}</UiButton>
      </span>
    </div>
    <p v-if="d.refreshError.value" class="dframe__note dframe__note--error" role="alert">
      The diff could not be read again: {{ visibleText(d.refreshError.value) }}
    </p>
    <slot />
  </template>
</template>

<style scoped>
.dframe__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.dframe__end {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  margin-left: auto;
}
.dframe__asof {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dframe__note {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.dframe__note--error {
  color: var(--omo-danger);
}
.dframe__line {
  margin: 0;
}
</style>
