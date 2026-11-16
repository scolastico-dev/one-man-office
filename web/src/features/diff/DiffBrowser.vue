<script setup lang="ts">
// The Diff tab on a ticket's page: the changed files as a folder tree on the
// left (per project when the ticket has several, each with its committed and
// uncommitted part; with a filter), and the chosen file's diff on the right,
// as on a pull request's "Files changed" page. One file at a time: the tree
// is the way through the files (with Previous and Next beside the diff), the
// same size rules as in the panel apply to that one file ("Show more", "Show
// anyway", a large file starts closed), and a diff of hundreds of files never
// draws more than one. The split is resizable and the tree can be hidden
// (both remembered); below 768 px the tree is a "Files (N)" button that opens
// it as a sheet. The panel shows the same diff stacked (DiffView); both read
// it through useDiff and draw files with DiffFileView.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { TicketSummary } from '../../api/types'
import Splitter from '../../components/ui/Splitter.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useBoard } from '../../store'
import { FILES_WIDTH, usePagePrefs } from '../ticket/page/pagePrefs'
import DiffFileView from './DiffFileView.vue'
import DiffFrame from './DiffFrame.vue'
import DiffProjectHead from './DiffProjectHead.vue'
import DiffTree from './DiffTree.vue'
import { diffTree, treeFiles, type TreeFile } from './fileTree'
import { fileKey, startsOpen, useDiff } from './useDiff'

const props = defineProps<{ ticket: TicketSummary }>()

const store = useBoard()
const d = useDiff(props.ticket.number)
const prefs = usePagePrefs()

const nameOf = (id: string) => visibleText(store.state.projects.get(id)?.name ?? id)
const projects = computed(() => d.diff.value?.projects ?? [])

const filter = ref('')
const options = { keyOf: fileKey, nameOf }
/** Every file, in the tree's order: the order of Previous and Next. */
const allFiles = computed<TreeFile[]>(() => treeFiles(diffTree(projects.value, options)))
const shownTree = computed(() => diffTree(projects.value, { ...options, filter: filter.value }))
const filtering = computed(() => filter.value.trim() !== '')
const matching = computed(() => (filtering.value ? treeFiles(shownTree.value).length : allFiles.value.length))

const selectedId = ref<string | null>(null)
// The first file shows at first; after a read the chosen file stays when it is still there.
watch(
  allFiles,
  (list) => {
    if (selectedId.value === null || !list.some((f) => f.id === selectedId.value)) selectedId.value = list[0]?.id ?? null
  },
  { immediate: true },
)
const selected = computed(() => allFiles.value.find((f) => f.id === selectedId.value) ?? null)
const index = computed(() => allFiles.value.findIndex((f) => f.id === selectedId.value))

// The project of the file shown is the diff's current project (its head, its remote switch, its reads).
watch(selected, (f) => {
  if (f && d.project.value !== f.project) d.project.value = f.project
})
const project = computed(() => (selected.value ? (projects.value.find((p) => p.project === selected.value!.project) ?? null) : d.current.value))

const shownFile = computed(() => (selected.value ? d.fileOf(selected.value.id, selected.value.file) : null))
const fileDomId = 'diff-browser-file'

// Narrow screens: the tree opens as a sheet.
const narrowQuery = typeof window.matchMedia === 'function' ? window.matchMedia('(max-width: 767.98px)') : null
const narrow = ref(narrowQuery?.matches ?? false)
const onNarrow = (e: MediaQueryListEvent) => (narrow.value = e.matches)
narrowQuery?.addEventListener('change', onNarrow)
onBeforeUnmount(() => narrowQuery?.removeEventListener('change', onNarrow))
const sheetOpen = ref(false)

const filesLabel = computed(() => `Files (${allFiles.value.length.toLocaleString('en')})`)

function open(id: string) {
  selectedId.value = id
  sheetOpen.value = false
}

function step(by: -1 | 1) {
  const next = allFiles.value[index.value + by]
  if (next) selectedId.value = next.id
}

function load() {
  const f = selected.value
  if (f) void d.loadFile(f.project, f.file.path, f.uncommitted)
}

const fmt = (n: number) => n.toLocaleString('en')
</script>

<template>
  <div class="dbrowse" :class="{ 'dbrowse--narrow': narrow, 'dbrowse--no-tree': !narrow && !prefs.files }" data-diff data-diff-browser>
    <DiffFrame :d="d">
      <template #bar>
        <UiButton v-if="narrow" size="sm" aria-haspopup="dialog" data-files-sheet @click="sheetOpen = true">{{ filesLabel }}</UiButton>
        <UiButton v-else size="sm" :aria-expanded="prefs.files ? 'true' : 'false'" data-toggle-files @click="prefs.files = !prefs.files">
          {{ prefs.files ? 'Hide files' : `Show files (${fmt(allFiles.length)})` }}
        </UiButton>
      </template>
      <p v-if="projects.length === 0" class="dbrowse__note">This ticket has no project, so there is nothing to compare.</p>
      <div v-else class="dbrowse__split" :style="!narrow && prefs.files ? { gridTemplateColumns: `${prefs.filesWidth}px auto minmax(0, 1fr)` } : undefined">
        <section v-if="!narrow && prefs.files" class="dbrowse__files" aria-label="Changed files" data-files-pane>
          <label class="sr-only" for="diff-browser-filter">Filter files</label>
          <input
            id="diff-browser-filter"
            v-model="filter"
            class="omo-input dbrowse__filter"
            type="search"
            placeholder="Filter files"
            autocomplete="off"
            spellcheck="false"
            data-files-filter
          />
          <p v-if="filtering" class="dbrowse__count" aria-live="polite">{{ matching === 0 ? 'No file matches.' : `${fmt(matching)} of ${fmt(allFiles.length)} files` }}</p>
          <div class="dbrowse__tree">
            <DiffTree :nodes="shownTree" :selected="selectedId" :label="`Changed files of #${ticket.number}`" :filtering="filtering" @open="open" />
          </div>
        </section>
        <Splitter
          v-if="!narrow && prefs.files"
          v-model="prefs.filesWidth"
          :min="FILES_WIDTH.min"
          :max="FILES_WIDTH.max"
          label="Width of the file list"
        />
        <section class="dbrowse__main" :aria-label="selected ? `Diff of ${visibleText(selected.file.path)}` : 'Diff'" data-file-pane>
          <div v-if="project" class="dbrowse__head" :data-diff-project="project.project">
            <DiffProjectHead :d="d" :project="project" :ticket="ticket" :name="nameOf(project.project)" />
          </div>
          <template v-if="selected && shownFile">
            <div class="dbrowse__file-bar">
              <p class="dbrowse__where" :data-diff-section="selected.uncommitted ? 'uncommitted' : 'committed'">
                {{ selected.uncommitted ? 'Uncommitted in the worktree' : 'Committed' }} · file {{ fmt(index + 1) }} of {{ fmt(allFiles.length) }}
              </p>
              <div class="dbrowse__steps">
                <UiButton size="sm" :disabled="index <= 0" data-prev-file @click="step(-1)">Previous file</UiButton>
                <UiButton size="sm" :disabled="index >= allFiles.length - 1" data-next-file @click="step(1)">Next file</UiButton>
              </div>
            </div>
            <ul class="dbrowse__list">
              <DiffFileView
                :key="selected.id"
                :file="shownFile"
                :dom-id="fileDomId"
                :open="d.isOpen(selected.id, startsOpen(shownFile, 1))"
                :loading="d.loadState(selected.id).loading"
                :loaded="d.loadState(selected.id).loaded"
                :error="d.loadState(selected.id).error"
                :uncommitted="selected.uncommitted"
                @toggle="d.setOpen(selected.id, $event)"
                @more="load"
              />
            </ul>
          </template>
          <p v-else-if="project && project.state === 'ok'" class="dbrowse__note">No commits of its own yet.</p>
        </section>
      </div>
    </DiffFrame>
    <UiDialog v-if="narrow" :open="sheetOpen" :title="filesLabel" data-files-dialog @close="sheetOpen = false">
      <div class="dbrowse__sheet">
        <label class="sr-only" for="diff-browser-filter-sheet">Filter files</label>
        <input
          id="diff-browser-filter-sheet"
          v-model="filter"
          class="omo-input dbrowse__filter"
          type="search"
          placeholder="Filter files"
          autocomplete="off"
          spellcheck="false"
          data-files-filter
        />
        <p v-if="filtering" class="dbrowse__count" aria-live="polite">{{ matching === 0 ? 'No file matches.' : `${fmt(matching)} of ${fmt(allFiles.length)} files` }}</p>
        <DiffTree :nodes="shownTree" :selected="selectedId" :label="`Changed files of #${ticket.number}`" :filtering="filtering" @open="open" />
      </div>
    </UiDialog>
  </div>
</template>

<style scoped>
/* Fills the tab's height on a wide page: tree and diff scroll on their own. */
.dbrowse {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  min-height: 0;
}
.dbrowse__split {
  display: grid;
  flex: 1 1 0;
  grid-template-columns: minmax(0, 1fr);
  min-height: calc(var(--omo-space) * 80);
  border-top: var(--omo-border) solid var(--omo-line);
}
.dbrowse__files {
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  min-height: 0;
  padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 2) 0 0;
}
.dbrowse__filter {
  width: 100%;
}
.dbrowse__count {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dbrowse__tree {
  flex: 1 1 0;
  min-height: 0;
  overflow: auto;
  padding-bottom: calc(var(--omo-space) * 3);
}
.dbrowse__main {
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  padding: calc(var(--omo-space) * 3) 0 calc(var(--omo-space) * 3) calc(var(--omo-space) * 2);
}
.dbrowse--no-tree .dbrowse__main,
.dbrowse--narrow .dbrowse__main {
  padding-left: 0;
}
.dbrowse__head {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.dbrowse__file-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.dbrowse__where {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.dbrowse__steps {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.dbrowse__list {
  margin: 0;
  padding: 0;
  list-style: none;
}
.dbrowse__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.dbrowse__sheet {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
/* A phone: no fixed heights, the page scrolls. */
.dbrowse--narrow .dbrowse__split {
  flex: none;
  min-height: 0;
}
.dbrowse--narrow .dbrowse__main {
  overflow: visible;
}
</style>
