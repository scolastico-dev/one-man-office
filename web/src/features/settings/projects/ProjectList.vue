<script setup lang="ts">
// The projects omo knows, from the board store (kept live by the
// `project.*` events), sorted by name, with a filter over name and folder.
// Below them: Add project, and Delete all missing projects, which lists them
// before it removes anything. A folder that disappears sends no event, so
// the list is read again when the page opens and after every scan, which
// counts the missing ones.
//
// Select… turns on a box per row: several projects are removed or switched
// off or on in one request (the server answers per project). Select all takes
// the rows the filter shows; shift-click and shift with an arrow key extend a
// range; Escape leaves. A refused project stays selected, with the server's
// sentence and, for one a workspace folder would find again, the ways out the
// single row offers. Below 768 px the actions are a bar at the bottom.
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { batchProjects, getSettings, removeMissingProjects } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { MergeModes, Project, ProjectBatchAction, SettingsChangedData } from '../../../api/types'
import FormSection from '../../../components/ui/FormSection.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import AddProjectDialog from '../../projects/AddProjectDialog.vue'
import { SETTINGS_NARROW } from '../settingsSections'
import ProjectRow from './ProjectRow.vue'
import { batchSummary, matchesFilter, projectsWord, rangeIds, selectionText } from './projectSelection'
import { sortProjects } from './useProjects'

const emit = defineEmits<{ ignore: [project: Project] }>()

const store = useBoard()
const session = useSession()
const announce = useAnnouncer()
const narrow = inject(SETTINGS_NARROW, ref(false))

/** The general merge defaults, for the words of "Use global default (…)". */
const defaults = ref<MergeModes | null>(null)
async function readDefaults() {
  try {
    defaults.value = (await getSettings(session.client)).data.settings.merge_defaults ?? null
  } catch {
    // The words fall back to what each project's answer says.
  }
}

// A change of the general settings may change the merge modes in effect of
// every project without a mode of its own, and sends no project event.
const offs = [
  store.onEvent('projects.scanned', () => void store.refreshProjects()),
  store.onEvent('settings.changed', (ev) => {
    if ((ev.data as Partial<SettingsChangedData> | null)?.section !== 'general') return
    void store.refreshProjects()
    void readDefaults()
  }),
]
onBeforeUnmount(() => offs.forEach((off) => off()))
void store.refreshProjects()
void readDefaults()

const projects = computed(() => sortProjects(store.state.projects.values()))
const missing = computed(() => projects.value.filter((p) => p.missing))

const query = ref('')
const shown = computed(() => projects.value.filter((p) => matchesFilter(p, query.value)))

// Multi-select. The selection holds ids; ids of projects that are gone drop out.
const selecting = ref(false)
const picked = ref<Set<string>>(new Set())
const selected = computed(() => projects.value.filter((p) => picked.value.has(p.id)))
const hiddenSelected = computed(() => selected.value.filter((p) => !matchesFilter(p, query.value)).length)
const countText = computed(() => selectionText(selected.value.length, hiddenSelected.value))
const allShownPicked = computed(() => shown.value.length > 0 && shown.value.every((p) => picked.value.has(p.id)))
const selectAllText = computed(() => (query.value.trim() === '' ? 'Select all' : `Select all ${shown.value.length} shown`))
/** The row the next shift-click or shift-arrow extends from. */
let anchor: string | null = null

watch(
  () => selected.value.length,
  (n, before) => {
    if (selecting.value && n !== before) announce.polite(selectionText(n, 0))
  },
)

function setPicked(next: Set<string>) {
  picked.value = next
}

function startSelecting() {
  selecting.value = true
  setPicked(new Set())
  anchor = null
  batch.value = null
}

function stopSelecting() {
  selecting.value = false
  setPicked(new Set())
  anchor = null
  batch.value = null
  void nextTick(() => listEl.value?.querySelector<HTMLElement>('[data-select-mode]')?.focus())
}

function selectAllShown() {
  const next = new Set(picked.value)
  for (const p of shown.value) next.add(p.id)
  setPicked(next)
}

function clearSelection() {
  setPicked(new Set())
}

/** A click on a row's box: toggles it, or with shift sets the range from the anchor to the state of the box. */
function pick(id: string, shift: boolean, on: boolean) {
  const next = new Set(picked.value)
  const ids = shift ? rangeIds(shown.value.map((p) => p.id), anchor, id) : [id]
  for (const x of ids) {
    if (on) next.add(x)
    else next.delete(x)
  }
  setPicked(next)
  anchor = id
}

const listEl = ref<HTMLElement>()
function boxOf(id: string) {
  return listEl.value?.querySelector<HTMLInputElement>(`input[data-project-select="${CSS.escape(id)}"]`) ?? null
}

/** Arrow keys on a box move to the next one; with shift they also select it (a range from the anchor). */
function onListKeydown(e: KeyboardEvent) {
  if (!selecting.value) return
  const target = e.target as HTMLElement
  if (e.key === 'Escape') {
    // A dialog, and a field with its own Escape (the name, a select), keep it.
    if (target.closest('dialog') || target.matches('input:not([type="checkbox"]):not([type="search"]), select, textarea')) return
    e.preventDefault()
    stopSelecting()
    return
  }
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
  const id = target.getAttribute('data-project-select')
  if (id === null) return
  const order = shown.value.map((p) => p.id)
  const i = order.indexOf(id)
  const j = e.key === 'ArrowDown' ? i + 1 : i - 1
  if (i < 0 || j < 0 || j >= order.length) return
  e.preventDefault()
  const to = order[j]!
  if (e.shiftKey) {
    if (anchor === null) anchor = id
    const next = new Set(picked.value)
    for (const x of rangeIds(order, anchor, to)) next.add(x)
    setPicked(next)
  }
  boxOf(to)?.focus()
}

// The answer of the last batch: a summary and the refused projects with their reasons.
interface Refused {
  project: Project
  message: string
  /** Refused because a workspace folder would find it again: the ways out apply. */
  waysOut: boolean
}
const batch = ref<{ summary: string; refused: Refused[] } | null>(null)
const busy = ref(false)
const confirmingRemove = ref(false)
const batchError = ref('')

async function run(action: ProjectBatchAction, list: Project[]): Promise<boolean> {
  if (busy.value || list.length === 0) return false
  busy.value = true
  batchError.value = ''
  try {
    const { data } = await batchProjects(
      session.client,
      list.map((p) => p.id),
      action,
    )
    const byId = new Map(list.map((p) => [p.id, p]))
    const refused: Refused[] = []
    let done = 0
    for (const r of data.results) {
      if (r.done) done++
      const p = byId.get(r.id)
      if (!r.done && p) refused.push({ project: p, message: visibleText(r.error?.message ?? 'Refused.'), waysOut: r.error?.code === 'validation' && p.root !== '' && action === 'remove' })
    }
    const summary = batchSummary(action, done, refused.length)
    batch.value = { summary, refused }
    if (action === 'remove') setPicked(new Set(refused.map((r) => r.project.id)))
    if (refused.length > 0) announce.assertive(summary)
    else announce.polite(summary)
    return true
  } catch (e) {
    batchError.value = visibleText(
      failureText(e, {
        lost: 'No answer from omo, so some projects may have changed. The list shows what omo stored.',
        refused: 'The change was refused.',
      }),
    )
    announce.assertive(batchError.value)
    return false
  } finally {
    busy.value = false
  }
}

function askRemove() {
  batchError.value = ''
  confirmingRemove.value = true
}

async function removeSelected() {
  const list = selected.value
  if (await run('remove', list)) confirmingRemove.value = false
}

/** The way out "Switch it off" for one refused project: it leaves the answer once done. */
async function switchOffRefused(r: Refused) {
  if (busy.value) return
  busy.value = true
  try {
    const { data } = await batchProjects(session.client, [r.project.id], 'disable')
    const item = data.results[0]
    if (item?.done) {
      if (batch.value) batch.value = { ...batch.value, refused: batch.value.refused.filter((x) => x !== r) }
      announce.polite(`${visibleText(r.project.name)} is switched off. New tickets cannot use it.`)
    } else {
      r.message = visibleText(item?.error?.message ?? 'Refused.')
      announce.assertive(r.message)
    }
  } catch (e) {
    r.message = visibleText(failureText(e, { lost: 'No answer from omo, so the project may have been switched off.', refused: 'The change was refused.' }))
    announce.assertive(r.message)
  } finally {
    busy.value = false
  }
}

// The bar of the actions is sticky at the bottom on a narrow screen, above the page's Save bar.
const saveBarHeight = ref(0)
let observer: ResizeObserver | null = null
onMounted(() => {
  const bar = listEl.value?.closest('[data-settings-page]')?.querySelector<HTMLElement>('.savebar')
  if (!bar || typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(() => (saveBarHeight.value = bar.offsetHeight))
  observer.observe(bar)
})
onBeforeUnmount(() => observer?.disconnect())

const adding = ref(false)
const asking = ref(false)
const removing = ref(false)
const removeError = ref('')

function askRemoveMissing() {
  removeError.value = ''
  asking.value = true
}

async function removeMissing() {
  if (removing.value) return
  removing.value = true
  removeError.value = ''
  try {
    const { data } = await removeMissingProjects(session.client)
    const n = data.removed.length
    announce.polite(n === 1 ? '1 missing project deleted.' : `${n} missing projects deleted.`)
    asking.value = false
  } catch (e) {
    removeError.value = visibleText(
      failureText(e, {
        lost: 'No answer from omo, so the projects may have been deleted. The list shows what omo stored.',
        refused: 'The missing projects could not be deleted.',
      }),
    )
    announce.assertive(removeError.value)
  } finally {
    removing.value = false
  }
}
</script>

<template>
  <FormSection legend="Projects" description="Switched off, a project keeps its tickets but takes no new ones.">
    <div ref="listEl" class="projects__list" data-projects-list @keydown="onListKeydown">
      <p v-if="projects.length === 0" class="m-0 text-muted" data-no-projects>No project yet. A project is a local git repository omo may work in.</p>
      <template v-else>
        <div class="projects__head">
          <div class="projects__filter">
            <TextField v-model="query" type="search" label="Filter projects" placeholder="Name or folder" autocomplete="off" spellcheck="false" data-project-filter />
          </div>
          <UiButton :aria-pressed="selecting ? 'true' : 'false'" data-select-mode @click="selecting ? stopSelecting() : startSelecting()">{{ selecting ? 'Done' : 'Select…' }}</UiButton>
        </div>
        <div v-if="selecting" class="projects__picking" role="group" aria-label="Selection" data-selection>
          <UiButton size="sm" :disabled="shown.length === 0 || allShownPicked" data-select-all @click="selectAllShown">{{ selectAllText }}</UiButton>
          <UiButton size="sm" :disabled="selected.length === 0" data-select-none @click="clearSelection">Clear selection</UiButton>
          <span class="projects__hint">Shift-click or shift with an arrow key selects a range; Escape leaves.</span>
        </div>
        <div v-if="selecting && !narrow" class="projects__bulk" role="group" aria-label="Selected projects" data-bulk>
          <span class="projects__count" data-selection-count>{{ countText }}</span>
          <UiButton size="sm" :disabled="busy || selected.length === 0" data-bulk-enable @click="run('enable', selected)">Switch on</UiButton>
          <UiButton size="sm" :disabled="busy || selected.length === 0" data-bulk-disable @click="run('disable', selected)">Switch off</UiButton>
          <UiButton size="sm" variant="danger" :disabled="busy || selected.length === 0" data-bulk-remove @click="askRemove">Remove…</UiButton>
        </div>
        <div v-if="batch || batchError" class="projects__result" data-batch-result>
          <p v-if="batch" class="m-0" data-batch-summary>{{ batch.summary }}</p>
          <p v-if="batchError" class="m-0 projects__failed" data-batch-error>{{ batchError }}</p>
          <ul v-if="batch && batch.refused.length > 0" class="projects__refused">
            <li v-for="r in batch.refused" :key="r.project.id" :data-batch-refused="r.project.id">
              <span class="projects__refused-name">{{ visibleText(r.project.name) }}</span>
              <span class="projects__failed">{{ r.message }}</span>
              <span v-if="r.waysOut" class="projects__buttons">
                <UiButton v-if="r.project.enabled" size="sm" :disabled="busy" @click="switchOffRefused(r)">Switch it off</UiButton>
                <UiButton size="sm" :disabled="busy" @click="emit('ignore', r.project)">Add an ignore pattern</UiButton>
              </span>
            </li>
          </ul>
        </div>
        <p v-if="shown.length === 0" class="m-0 text-muted" data-no-match>No project matches “{{ visibleText(query.trim()) }}”.</p>
        <div v-else class="projects__scroll">
          <table class="projects" :class="{ 'projects--selecting': selecting }" data-projects>
            <thead>
              <tr>
                <th v-if="selecting" scope="col"><span class="sr-only">Selected</span></th>
                <th scope="col">Name</th>
                <th scope="col">Folder</th>
                <th scope="col">Branch</th>
                <th scope="col">State</th>
                <th scope="col"><span class="sr-only">Change</span></th>
              </tr>
            </thead>
            <tbody>
              <ProjectRow
                v-for="p in shown"
                :key="p.id"
                :project="p"
                :defaults="defaults"
                :selecting="selecting"
                :selected="picked.has(p.id)"
                @pick="(shift: boolean, on: boolean) => pick(p.id, shift, on)"
                @ignore="emit('ignore', $event)"
              />
            </tbody>
          </table>
        </div>
        <div v-if="selecting && narrow" class="projects__bulk projects__bulk--bar" :style="{ bottom: `${saveBarHeight}px` }" role="group" aria-label="Selected projects" data-bulk>
          <span class="projects__count" data-selection-count>{{ countText }}</span>
          <UiButton size="sm" :disabled="busy || selected.length === 0" data-bulk-enable @click="run('enable', selected)">Switch on</UiButton>
          <UiButton size="sm" :disabled="busy || selected.length === 0" data-bulk-disable @click="run('disable', selected)">Switch off</UiButton>
          <UiButton size="sm" variant="danger" :disabled="busy || selected.length === 0" data-bulk-remove @click="askRemove">Remove…</UiButton>
        </div>
      </template>
    </div>
    <div class="projects__footer">
      <UiButton data-add-project @click="adding = true">Add project</UiButton>
      <UiButton v-if="missing.length > 0" size="sm" variant="danger" data-remove-missing @click="askRemoveMissing">Delete all missing projects…</UiButton>
    </div>
    <AddProjectDialog :open="adding" @close="adding = false" />
    <UiDialog :open="confirmingRemove" :title="`Remove ${projectsWord(selected.length)}?`" eyebrow="projects" @close="confirmingRemove = false">
      <div class="grid gap-3" data-batch-remove-dialog>
        <p class="m-0">Remove {{ projectsWord(selected.length) }} from omo? The folders stay as they are; tickets keep their history.</p>
        <ul class="projects__gone projects__gone--scroll" tabindex="0" aria-label="Projects to remove" data-batch-remove-list>
          <li v-for="p in selected" :key="p.id">
            <span class="projects__gone-name">{{ visibleText(p.name) }}</span>
            <span class="projects__gone-path">{{ visibleText(p.path) }}</span>
          </li>
        </ul>
        <p v-if="batchError" class="omo-field__error" data-batch-remove-error>{{ batchError }}</p>
      </div>
      <template #actions>
        <UiButton @click="confirmingRemove = false">Cancel</UiButton>
        <UiButton variant="danger" :disabled="busy || selected.length === 0" data-batch-remove-confirm @click="removeSelected">Remove {{ projectsWord(selected.length) }}</UiButton>
      </template>
    </UiDialog>
    <UiDialog :open="asking" title="Delete all missing projects?" eyebrow="projects" @close="asking = false">
      <div class="grid gap-3" data-remove-missing-dialog>
        <p class="m-0">The folders of these projects are gone. omo forgets them; their tickets keep their history.</p>
        <ul class="projects__gone">
          <li v-for="p in missing" :key="p.id">
            <span class="projects__gone-name">{{ visibleText(p.name) }}</span>
            <span class="projects__gone-path">{{ visibleText(p.path) }}</span>
          </li>
        </ul>
        <p v-if="removeError" class="omo-field__error" data-remove-missing-error>{{ removeError }}</p>
      </div>
      <template #actions>
        <UiButton @click="asking = false">Cancel</UiButton>
        <UiButton variant="danger" :disabled="removing || missing.length === 0" data-remove-missing-confirm @click="removeMissing">
          {{ missing.length === 1 ? 'Delete 1 project' : `Delete ${missing.length} projects` }}
        </UiButton>
      </template>
    </UiDialog>
  </FormSection>
</template>

<style scoped>
.projects__list {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.projects__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
}
.projects__filter {
  flex: 1 1 calc(var(--omo-space) * 60);
  min-width: 0;
}
.projects__picking,
.projects__bulk {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.projects__hint {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.projects__bulk {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-bg);
}
.projects__count {
  margin-right: auto;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
/* Narrow screens: the actions stick to the bottom of the page, above its Save bar. */
.projects__bulk--bar .projects__count {
  flex-basis: 100%;
}
.projects__bulk--bar {
  position: sticky;
  z-index: 3;
  bottom: 0;
  box-shadow: 0 calc(var(--omo-space) * -1) 0 var(--omo-surface);
}
.projects__result {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-accent);
  background: var(--omo-bg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.projects__refused {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.projects__refused li {
  display: grid;
  gap: var(--omo-space);
  overflow-wrap: anywhere;
}
.projects__refused-name {
  font-weight: 700;
}
.projects__failed {
  color: var(--omo-danger);
}
.projects__scroll {
  min-width: 0;
}
.projects :deep(.projects__pick) {
  width: calc(var(--omo-space) * 6);
}
.projects :deep(.projects__box) {
  width: calc(var(--omo-space) * 4);
  height: calc(var(--omo-space) * 4);
  margin: 2px 0 0;
  accent-color: var(--omo-accent);
}
.projects {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.projects :deep(th),
.projects :deep(td) {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3) calc(var(--omo-space) * 2) 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
  text-align: left;
  vertical-align: top;
}
.projects thead th {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
}
.projects :deep(tbody th) {
  font-weight: 700;
  overflow-wrap: anywhere;
}
.projects :deep(.projects__path),
.projects :deep(.projects__branch) {
  overflow-wrap: anywhere;
}
.projects :deep(.projects__folder),
.projects :deep(.projects__origin),
.projects :deep(.projects__missing),
.projects :deep(.projects__notice) {
  display: block;
}
.projects :deep(.projects__override) {
  display: block;
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.projects :deep(.projects__origin) {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.projects :deep(.projects__missing) {
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.projects :deep(.projects__use) {
  white-space: nowrap;
}
.projects :deep(.projects__use > *) {
  vertical-align: middle;
}
.projects :deep(.projects__state) {
  display: inline-block;
  min-width: 8ch;
}
.projects :deep(.projects__state[data-state='missing']) {
  color: var(--omo-danger);
}
.projects :deep(.projects__state[data-state='disabled']) {
  color: var(--omo-muted);
}
.projects :deep(.projects__actions) {
  padding-right: 0;
  text-align: right;
}
.projects :deep(.projects__more > td) {
  padding-top: 0;
}
.projects :deep(.projects__form) {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
}
.projects :deep(.projects__confirm),
.projects :deep(.projects__message) {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
.projects :deep(.projects__message) {
  color: var(--omo-danger);
  overflow-wrap: anywhere;
}
.projects :deep(.projects__buttons),
.projects__buttons,
.projects__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.projects :deep(code),
.projects__gone code {
  font-family: inherit;
  color: var(--omo-accent);
}
.projects__gone {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.projects__gone li {
  display: grid;
  overflow-wrap: anywhere;
}
.projects__gone--scroll {
  max-height: min(40vh, calc(var(--omo-space) * 80));
  overflow-y: auto;
  padding-right: calc(var(--omo-space) * 2);
}
.projects__gone-path {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}

/* Narrow screens: each project is a block of its own instead of a row of columns. */
@media (max-width: 767.98px) {
  .projects thead {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  .projects :deep(tr) {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    column-gap: calc(var(--omo-space) * 3);
    padding: calc(var(--omo-space) * 2) 0;
    border-bottom: var(--omo-border) solid var(--omo-line);
  }
  .projects :deep(tbody th),
  .projects :deep(tbody td) {
    display: block;
    grid-column: 1;
    padding: 0;
    border-bottom: 0;
  }
  .projects :deep(tbody .projects__use) {
    grid-column: 2;
    grid-row: 1;
  }
  .projects :deep(tbody .projects__actions) {
    grid-column: 2;
    grid-row: 2;
  }
  .projects :deep(tbody .projects__more > td) {
    grid-column: 1 / -1;
  }
  /* With a box per row: the box is a column of its own on the left. */
  .projects--selecting :deep(tr) {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }
  .projects--selecting :deep(tbody th),
  .projects--selecting :deep(tbody td) {
    grid-column: 2;
  }
  .projects--selecting :deep(tbody .projects__pick) {
    grid-column: 1;
    grid-row: 1 / span 3;
    width: auto;
  }
  .projects--selecting :deep(tbody .projects__use) {
    grid-column: 3;
  }
  .projects--selecting :deep(tbody .projects__actions) {
    grid-column: 3;
  }
  .projects :deep(.projects__more) {
    padding-top: 0;
  }
}
</style>
