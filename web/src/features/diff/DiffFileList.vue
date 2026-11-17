<script setup lang="ts">
// The files of one section of a diff as a list of jumps: choosing one opens
// that file below and moves the focus to it.
import type { DiffFile } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'

export interface FileEntry {
  key: string
  domId: string
  file: DiffFile
}

defineProps<{
  entries: FileEntry[]
  /** Accessible name of the list. */
  label: string
}>()
const emit = defineEmits<{ jump: [key: string] }>()
</script>

<template>
  <nav class="dlist" :aria-label="label">
    <ul class="dlist__items">
      <li v-for="e in entries" :key="e.key" class="dlist__item">
        <button type="button" class="dlist__jump" :aria-controls="e.domId" data-file-jump @click="emit('jump', e.key)">{{ visibleText(e.file.path) }}</button>
        <span v-if="e.file.binary" class="dlist__note">binary</span>
        <span v-else-if="e.file.status === 'untracked'" class="dlist__note">untracked</span>
        <span v-else class="dlist__counts"
          ><span class="dlist__add">+{{ e.file.additions }}</span> <span class="dlist__del">−{{ e.file.deletions }}</span></span
        >
      </li>
    </ul>
  </nav>
</template>

<style scoped>
.dlist__items {
  display: grid;
  gap: var(--omo-space);
  margin: 0;
  padding: 0;
  list-style: none;
}
.dlist__item {
  display: flex;
  align-items: baseline;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dlist__jump {
  flex: 0 1 auto;
  min-width: 0;
  padding: 0;
  border: 0;
  background: none;
  color: var(--omo-accent);
  font: inherit;
  text-align: left;
  text-decoration: underline;
  overflow-wrap: anywhere;
  cursor: pointer;
}
.dlist__counts,
.dlist__note {
  flex-shrink: 0;
}
.dlist__note {
  color: var(--omo-muted);
}
.dlist__add {
  color: var(--omo-ok);
}
.dlist__del {
  color: var(--omo-danger);
}
</style>
