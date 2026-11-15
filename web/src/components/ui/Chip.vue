<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    title?: string
    /** `button` makes the chip clickable (listen to `click`). */
    as?: 'span' | 'button'
  }>(),
  { title: undefined, as: 'span' },
)
</script>

<template>
  <button v-if="as === 'button'" type="button" class="chip chip--button" :title="title">
    <span class="chip__text">{{ label }}</span>
  </button>
  <span v-else class="chip" :title="title"><span class="chip__text">{{ label }}</span></span>
</template>

<style scoped>
.chip {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  min-width: 0;
  padding: 0 calc(var(--omo-space) * 1.5);
  border: var(--omo-border) solid var(--omo-line);
  background: transparent;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  vertical-align: middle;
}
.chip__text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chip--button {
  border-color: var(--omo-line-strong);
  cursor: pointer;
  transition:
    border-color var(--omo-duration) ease,
    color var(--omo-duration) ease;
}
@media (hover: hover) {
  .chip--button:hover {
    border-color: var(--omo-accent);
    color: var(--omo-fg);
  }
}
</style>
