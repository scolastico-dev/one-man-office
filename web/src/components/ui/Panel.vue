<script setup lang="ts">
import { computed, useId } from 'vue'

const props = withDefaults(
  defineProps<{
    /** Shown in the top border; also the accessible name of the panel. */
    label: string
    /** Element to render, `section` by default. */
    as?: string
    /** Id of the label heading, for elements outside that refer to it. */
    labelId?: string
    /** Optional count shown after the label in the accent colour, part of the name. */
    count?: number
    /** Shown as `count of total` when it differs from `count`: a filtered count. */
    total?: number
    /** Inner padding; switch off when the content brings its own. */
    padded?: boolean
  }>(),
  { as: 'section', labelId: undefined, count: undefined, total: undefined, padded: true },
)

const autoId = useId()
const headingId = computed(() => props.labelId ?? `panel-${autoId}`)
</script>

<template>
  <component :is="as" class="panel" :class="{ 'panel--padded': padded }" :aria-labelledby="headingId">
    <div class="panel__head">
      <h2 :id="headingId" class="panel__label">
        {{ label }}<span v-if="count !== undefined" class="panel__count">{{ total !== undefined && total !== count ? ` ${count} of ${total}` : ` ${count}` }}</span>
      </h2>
      <div v-if="$slots.actions" class="panel__actions"><slot name="actions" /></div>
    </div>
    <slot />
  </component>
</template>

<style scoped>
/* A square frame whose label interrupts the top border, like a terminal window title. */
.panel {
  position: relative;
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
  transition: border-color var(--omo-duration) ease;
  /* Behind the label: covers the border line, leaves the page above it visible. */
  --panel-cut: linear-gradient(
    to bottom,
    transparent calc(50% - var(--omo-border)),
    var(--omo-surface) calc(50% - var(--omo-border))
  );
}
.panel--padded {
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 3) calc(var(--omo-space) * 3);
}
.panel__head {
  position: absolute;
  z-index: 1;
  top: 0;
  left: calc(var(--omo-space) * 2.5);
  right: calc(var(--omo-space) * 2.5);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  transform: translateY(-50%);
  pointer-events: none;
}
.panel__label {
  min-width: 0;
  margin: 0;
  padding: 0 calc(var(--omo-space) * 1.5);
  overflow: hidden;
  background: var(--panel-cut);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: color var(--omo-duration) ease;
}
.panel__count {
  color: var(--omo-accent);
}
.panel__actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--omo-space);
  padding: 0 var(--omo-space);
  background: var(--panel-cut);
  pointer-events: auto;
}
.panel:focus-within {
  border-color: var(--omo-accent);
}
.panel:focus-within > .panel__head > .panel__label {
  color: var(--omo-accent);
}
@media (hover: hover) {
  .panel:hover {
    border-color: var(--omo-accent);
  }
  .panel:hover > .panel__head > .panel__label {
    color: var(--omo-accent);
  }
}
</style>
