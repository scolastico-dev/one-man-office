<script setup lang="ts">
// The folder picker with static data: a path field, Go up, the folders of the
// path shown (repositories and links marked, hidden folders behind a switch)
// and "Choose this folder". A mock-up for the real dialog to match.
import { computed, ref } from 'vue'
import Chip from '../components/ui/Chip.vue'
import TextField from '../components/ui/TextField.vue'
import Toggle from '../components/ui/Toggle.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiDialog from '../components/ui/UiDialog.vue'
import { visibleText } from '../components/ui/visibleText'
import { sampleFolders, samplePickerPath } from './sampleSettings'

defineProps<{ open: boolean; title: string }>()
const emit = defineEmits<{ close: []; choose: [path: string] }>()

const path = ref(samplePickerPath)
const typed = ref(samplePickerPath)
const showHidden = ref(false)
const folders = computed(() => sampleFolders.filter((f) => showHidden.value || !f.hidden))
const hiddenCount = computed(() => sampleFolders.filter((f) => f.hidden).length)

function go(to: string) {
  path.value = to
  typed.value = to
}
function up() {
  const cut = path.value.replace(/\/+$/, '').lastIndexOf('/')
  go(cut > 0 ? path.value.slice(0, cut) : '/')
}
</script>

<template>
  <UiDialog :open="open" :title="title" eyebrow="folder" @close="emit('close')">
    <div class="picker">
      <form class="picker__path" @submit.prevent="go(typed.trim())">
        <TextField v-model="typed" label="Folder" hint="Type a path and press Enter, or open a folder below." spellcheck="false" autocomplete="off" />
        <UiButton type="button" class="picker__up" @click="up">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M2 6l4-4 4 4M6 2v9" />
          </svg>
          Go up
        </UiButton>
      </form>
      <div class="picker__bar">
        <p class="picker__count">{{ folders.length }} folders in <span class="picker__where">{{ visibleText(path) }}</span></p>
        <Toggle v-model="showHidden" :label="`Show hidden folders (${hiddenCount})`" />
      </div>
      <ul class="picker__list" aria-label="Folders">
        <li v-for="f in folders" :key="f.name">
          <button type="button" class="picker__folder" @click="go(`${path}/${f.name}`)">
            <svg class="picker__icon" width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.2">
              <path d="M1.5 3h4l1.5 1.5h5.5v7h-11z" />
            </svg>
            <span class="picker__name">{{ visibleText(f.name) }}</span>
            <Chip v-if="f.repository" label="git repository" />
            <Chip v-if="f.link" label="link" />
          </button>
        </li>
      </ul>
    </div>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton variant="primary" @click="emit('choose', path)">Choose this folder</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.picker {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.picker__path {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the input: the label above it is one line of small text plus the field's gap. */
.picker__up {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
  min-height: calc(var(--omo-space) * 10);
}
.picker__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
}
.picker__count {
  min-width: 0;
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.picker__where {
  color: var(--omo-fg);
}
.picker__list {
  max-height: calc(var(--omo-space) * 80);
  margin: 0;
  padding: 0;
  overflow-y: auto;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
  list-style: none;
}
.picker__list li + li {
  border-top: var(--omo-border) solid var(--omo-line);
}
.picker__folder {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  width: 100%;
  min-height: calc(var(--omo-space) * 10);
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  border: 0;
  background: transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: left;
  cursor: pointer;
}
.picker__folder:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.picker__icon {
  flex-shrink: 0;
  color: var(--omo-muted);
}
.picker__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (hover: hover) {
  .picker__folder:hover {
    background: var(--omo-accent-soft);
  }
  .picker__folder:hover .picker__icon {
    color: var(--omo-accent);
  }
}
</style>
