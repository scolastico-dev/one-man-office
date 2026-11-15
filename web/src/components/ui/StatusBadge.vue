<script setup lang="ts">
withDefaults(
  defineProps<{
    tone: 'danger' | 'warn' | 'accent' | 'muted' | 'ok'
    /** The status in words; colour only reinforces it. */
    text: string
    title?: string
  }>(),
  { title: undefined },
)
</script>

<template>
  <span class="badge" :class="`badge--${tone}`" :title="title"
    ><span v-if="$slots.icon" class="badge__icon" aria-hidden="true"><slot name="icon" /></span
    ><span class="badge__text">{{ text }}</span></span
  >
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: var(--omo-space);
  max-width: 100%;
  padding: 0 calc(var(--omo-space) * 1.5);
  border: var(--omo-border) solid currentColor;
  overflow: hidden;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
.badge__icon {
  display: inline-flex;
  flex-shrink: 0;
}
.badge__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.badge--danger {
  background: var(--omo-danger-soft);
  color: var(--omo-danger);
}
.badge--warn {
  color: var(--omo-warn);
}
.badge--accent {
  color: var(--omo-accent);
}
.badge--ok {
  color: var(--omo-ok);
}
.badge--muted {
  border-color: var(--omo-line-strong);
  color: var(--omo-muted);
}
</style>
