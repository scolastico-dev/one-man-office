<script setup lang="ts">
// The projects omo knows, from the board store (kept live by the
// `project.*` events), sorted by name. Below them: Add project, and Delete
// all missing projects, which lists them before it removes anything.
// A folder that disappears sends no event, so the list is read again when
// the page opens and after every scan, which counts the missing ones.
import { computed, onBeforeUnmount, ref } from 'vue'
import { removeMissingProjects } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { Project } from '../../../api/types'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import AddProjectDialog from '../../projects/AddProjectDialog.vue'
import ProjectRow from './ProjectRow.vue'
import { sortProjects } from './useProjects'

const emit = defineEmits<{ ignore: [project: Project] }>()

const store = useBoard()
const session = useSession()
const announce = useAnnouncer()

const offScanned = store.onEvent('projects.scanned', () => void store.refreshProjects())
onBeforeUnmount(offScanned)
void store.refreshProjects()

const projects = computed(() => sortProjects(store.state.projects.values()))
const missing = computed(() => projects.value.filter((p) => p.missing))

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
    <p v-if="projects.length === 0" class="m-0 text-muted" data-no-projects>No project yet. A project is a local git repository omo may work in.</p>
    <div v-else class="projects__scroll">
      <table class="projects" data-projects>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Folder</th>
            <th scope="col">Branch</th>
            <th scope="col">State</th>
            <th scope="col"><span class="sr-only">Change</span></th>
          </tr>
        </thead>
        <tbody>
          <ProjectRow v-for="p in projects" :key="p.id" :project="p" @ignore="emit('ignore', $event)" />
        </tbody>
      </table>
    </div>
    <div class="projects__footer">
      <UiButton data-add-project @click="adding = true">Add project</UiButton>
      <UiButton v-if="missing.length > 0" size="sm" variant="danger" data-remove-missing @click="askRemoveMissing">Delete all missing projects…</UiButton>
    </div>
    <AddProjectDialog :open="adding" @close="adding = false" />
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
.projects__scroll {
  min-width: 0;
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
  .projects :deep(.projects__more) {
    padding-top: 0;
  }
}
</style>
