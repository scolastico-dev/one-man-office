<script setup lang="ts">
import { computed, useId, useSlots } from 'vue'

const props = withDefaults(
  defineProps<{
    /** The name of the group, shown in the top border like a panel's label. */
    legend: string
    /** One or two sentences under the legend; the `description` slot replaces it. */
    description?: string
    /** Level of the heading inside the legend, so the page outline lists the section. */
    level?: 2 | 3 | 4
  }>(),
  { description: undefined, level: 2 },
)

const slots = useSlots()
const descriptionId = useId()
const hasDescription = computed(() => Boolean(slots.description || props.description))
</script>

<template>
  <fieldset class="form-section" :aria-describedby="hasDescription ? descriptionId : undefined">
    <legend class="form-section__legend">
      <component :is="`h${level}`" class="form-section__title">{{ legend }}</component>
    </legend>
    <div v-if="hasDescription" :id="descriptionId" class="form-section__description">
      <slot name="description">{{ description }}</slot>
    </div>
    <div class="form-section__body"><slot /></div>
  </fieldset>
</template>

<style scoped>
/* A group of fields in a square frame whose legend cuts the top border, like a Panel. */
.form-section {
  min-width: 0;
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3) calc(var(--omo-space) * 4);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
  transition: border-color var(--omo-duration) ease;
}
.form-section__legend {
  max-width: calc(100% - var(--omo-space) * 2);
  margin-left: calc(var(--omo-space) * -0.5);
  padding: 0 calc(var(--omo-space) * 1.5);
}
.form-section__title {
  margin: 0;
  overflow: hidden;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: color var(--omo-duration) ease;
}
.form-section__description {
  margin: 0 0 calc(var(--omo-space) * 4);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.form-section__description :deep(p) {
  margin: 0;
}
.form-section__description :deep(p + p) {
  margin-top: var(--omo-space);
}
.form-section__body {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
}
.form-section:focus-within {
  border-color: var(--omo-accent);
}
.form-section:focus-within > .form-section__legend > .form-section__title {
  color: var(--omo-accent);
}
@media (hover: hover) {
  .form-section:hover {
    border-color: var(--omo-accent);
  }
  .form-section:hover > .form-section__legend > .form-section__title {
    color: var(--omo-accent);
  }
}
</style>
