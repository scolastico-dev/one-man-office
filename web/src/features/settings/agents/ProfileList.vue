<script setup lang="ts">
// The stored agent profiles, one row each: name, program, command and how
// many models it has. Choosing a row opens its form below. Profile files are
// files in omo's home that anyone (an agent too) can write, so names and
// commands are shown as visible text.
import type { Profile } from '../../../api/types'
import { visibleText } from '../../../components/ui/visibleText'
import { cliLabel } from './profileForm'

defineProps<{
  profiles: readonly Profile[]
  /** The profile whose form is open. */
  selected: string | null
}>()
const emit = defineEmits<{ select: [name: string] }>()

const count = (p: Profile) => {
  const n = Object.keys(p.models ?? {}).length
  return n === 1 ? '1 model' : `${n} models`
}
</script>

<template>
  <ul class="profiles" data-profile-list>
    <li v-for="p in profiles" :key="p.name" class="profiles__item">
      <button
        type="button"
        class="profiles__row"
        :aria-current="p.name === selected ? 'true' : undefined"
        :data-profile="p.name"
        @click="emit('select', p.name)"
      >
        <span class="profiles__name">{{ visibleText(p.name) }}</span>
        <span class="profiles__meta">{{ visibleText(cliLabel(p.cli)) }} · {{ count(p) }}</span>
        <span class="profiles__command">{{ visibleText(p.command) }}</span>
      </button>
    </li>
  </ul>
</template>

<style scoped>
.profiles {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: var(--omo-border) solid var(--omo-line);
}
.profiles__item {
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.profiles__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--omo-space) calc(var(--omo-space) * 4);
  width: 100%;
  padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 3);
  border: 0;
  border-left: var(--omo-edge) solid transparent;
  background: transparent;
  color: var(--omo-fg);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.profiles__row[aria-current='true'] {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.profiles__name {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.profiles__meta,
.profiles__command {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.profiles__command {
  grid-column: 1 / -1;
  overflow-wrap: anywhere;
}
@media (hover: hover) {
  .profiles__row:hover {
    background: var(--omo-raised);
  }
}
</style>
