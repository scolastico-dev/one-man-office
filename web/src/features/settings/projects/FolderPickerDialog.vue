<script setup lang="ts">
// Chooses a folder on the computer omo runs on: a path field (Enter opens
// it), Go up, the sub-folders of the folder shown (git repositories and links
// marked, hidden folders behind a switch) and "Choose this folder". omo lists
// folder names only, never files. Names come escaped from the server; paths
// are exact, so they are shown through visibleText and sent back as they are.
import { computed, nextTick, ref, watch } from 'vue'
import { ApiError } from '../../../api/client'
import { listFolders } from '../../../api/endpoints'
import type { FolderEntry, FolderListing } from '../../../api/types'
import Chip from '../../../components/ui/Chip.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'

const props = defineProps<{
  open: boolean
  title: string
  /** The folder shown first; the home folder when empty. */
  start?: string
}>()
const emit = defineEmits<{ close: []; choose: [path: string] }>()

const session = useSession()
const announce = useAnnouncer()

const listing = ref<FolderListing | null>(null)
const typed = ref('')
const showHidden = ref(false)
const error = ref('')
const loading = ref(false)
const list = ref<HTMLElement>()
const chooseButton = ref<InstanceType<typeof UiButton>>()

const folders = computed<FolderEntry[]>(() => (listing.value?.folders ?? []).filter((f) => showHidden.value || !f.hidden))
const hiddenCount = computed(() => (listing.value?.folders ?? []).filter((f) => f.hidden).length)

/** Numbers of listings: only the answer to the newest request is shown. */
let seq = 0

/** The words for a refused listing of `path`. */
function refusal(e: unknown, path: string): string {
  if (e instanceof ApiError) {
    if (e.status === 404) return path === '' ? 'The home folder does not exist.' : `There is no folder ${visibleText(path)}.`
    if (e.status === 503) return 'The folder did not answer in time. Try again, or open another folder.'
    if (e.message !== '') return visibleText(e.message)
  }
  return e instanceof Error && e.message !== '' ? e.message : 'The folder could not be listed.'
}

async function load(path: string, how: 'typed' | 'list' = 'typed') {
  const mine = ++seq
  loading.value = true
  try {
    const { data } = await listFolders(session.client, path === '' ? undefined : path)
    if (mine !== seq) return
    listing.value = data
    typed.value = data.path
    error.value = ''
    announce.polite(`${countText(data)} in ${visibleText(data.path)}.`)
    if (how === 'list') {
      await nextTick()
      const first = list.value?.querySelector<HTMLElement>('button')
      if (first) first.focus()
      else (chooseButton.value?.$el as HTMLElement | undefined)?.focus()
    }
  } catch (e) {
    if (mine !== seq) return
    error.value = refusal(e, path)
    announce.assertive(error.value)
  } finally {
    if (mine === seq) loading.value = false
  }
}

function countText(l: FolderListing): string {
  const n = l.folders.filter((f) => showHidden.value || !f.hidden).length
  return n === 1 ? '1 folder' : `${n} folders`
}

watch(
  () => props.open,
  (open) => {
    if (!open) {
      seq++
      return
    }
    listing.value = null
    error.value = ''
    showHidden.value = false
    typed.value = props.start ?? ''
    void load(props.start ?? '')
  },
  { immediate: true },
)

function goUp() {
  const parent = listing.value?.parent
  if (parent) void load(parent, 'list')
}

function choose() {
  if (listing.value) emit('choose', listing.value.path)
}
</script>

<template>
  <UiDialog :open="open" :title="title" eyebrow="folder" @close="emit('close')">
    <div class="picker" data-folder-picker>
      <form class="picker__path" @submit.prevent="load(typed.trim())">
        <TextField
          v-model="typed"
          label="Folder"
          name="folder"
          hint="Type a path and press Enter, or open a folder below."
          :error="error"
          spellcheck="false"
          autocomplete="off"
          data-picker-path
        />
        <UiButton type="button" class="picker__up" :disabled="!listing?.parent" data-picker-up @click="goUp">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M2 6l4-4 4 4M6 2v9" />
          </svg>
          Go up
        </UiButton>
      </form>
      <template v-if="listing">
        <div class="picker__bar">
          <p class="picker__count" data-picker-where>
            {{ folders.length === 1 ? '1 folder' : `${folders.length} folders` }} in
            <span class="picker__where">{{ visibleText(listing.path) }}</span>
          </p>
          <Toggle v-model="showHidden" :label="`Show hidden folders (${hiddenCount})`" data-picker-hidden />
        </div>
        <ul v-if="folders.length > 0" ref="list" class="picker__list" aria-label="Folders" :aria-busy="loading ? 'true' : undefined">
          <li v-for="f in folders" :key="f.path">
            <button type="button" class="picker__folder" :data-folder="f.name" @click="load(f.path, 'list')">
              <svg class="picker__icon" width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.2">
                <path d="M1.5 3h4l1.5 1.5h5.5v7h-11z" />
              </svg>
              <span class="picker__name">{{ visibleText(f.name) }}</span>
              <Chip v-if="f.repository" label="git repository" />
              <Chip v-if="f.link" label="link" />
            </button>
          </li>
        </ul>
        <p v-else class="picker__empty">{{ hiddenCount > 0 ? 'Only hidden folders here.' : 'No folders here.' }}</p>
        <p v-if="listing.more" class="picker__empty">Only the first 2,000 folders are listed. Type a longer path to get further.</p>
      </template>
      <p v-else-if="loading" class="picker__empty">Listing the folders…</p>
    </div>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton ref="chooseButton" variant="primary" :disabled="!listing" data-primary data-picker-choose @click="choose">Choose this folder</UiButton>
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
.picker__count,
.picker__empty {
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
