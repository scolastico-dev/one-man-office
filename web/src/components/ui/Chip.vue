<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    title?: string
    /** `button` makes the chip clickable (listen to `click`). */
    as?: 'span' | 'button'
    /** For a button chip that is switched on or off: whether it is on (shown with a check mark and `aria-pressed`). */
    pressed?: boolean
  }>(),
  { title: undefined, as: 'span', pressed: undefined },
)
</script>

<template>
  <button
    v-if="as === 'button'"
    type="button"
    class="chip chip--button"
    :class="{ 'chip--on': pressed === true }"
    :title="title"
    :aria-pressed="pressed === undefined ? undefined : pressed ? 'true' : 'false'"
  >
    <svg v-if="pressed === true" class="chip__check" width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M2 6.5l2.5 2.5L10 3.5" />
    </svg>
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
.chip--on {
  gap: var(--omo-space);
  border-color: var(--omo-accent);
  background: var(--omo-accent-soft);
  color: var(--omo-fg);
}
.chip__check {
  flex: none;
  color: var(--omo-accent);
}
@media (hover: hover) {
  .chip--button:hover {
    border-color: var(--omo-accent);
    color: var(--omo-fg);
  }
}
</style>
