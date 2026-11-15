<script setup lang="ts">
// The Projects section of the settings: the known projects, and the button
// that adds one. Names and paths can be written by agents (a project file is
// a file in omo's home), so they are shown as visible text.
import { computed, ref } from 'vue'
import type { Project } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useBoard } from '../../../store'
import { branchNotice } from '../../create/branchNotice'
import AddProjectDialog from '../../projects/AddProjectDialog.vue'
import { projectState } from '../../projects/projectState'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('projects')
const store = useBoard()
const adding = ref(false)

const projects = computed<Project[]>(() =>
  [...store.state.projects.values()].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id)),
)
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <template #actions>
      <UiButton variant="primary" @click="adding = true">Add project</UiButton>
    </template>
    <p v-if="projects.length === 0" class="m-0 text-muted">No project yet. A project is a local git repository omo may work in.</p>
    <div v-else class="projects__scroll">
      <table class="projects">
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Path</th>
            <th scope="col">Branch</th>
            <th scope="col">State</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in projects" :key="p.id" :data-project="p.id">
            <th scope="row">{{ visibleText(p.name) }}</th>
            <td class="projects__path">{{ visibleText(p.path) }}</td>
            <td class="projects__branch">
              <code v-if="p.current_branch">{{ visibleText(p.current_branch) }}</code>
              <span v-if="branchNotice(p)" class="omo-field__hint projects__notice" data-branch-notice>
                Not its default branch <code>{{ visibleText(p.default_branch ?? '') }}</code>.
              </span>
            </td>
            <td>
              <span class="projects__state" :data-state="projectState(p)">{{ projectState(p) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <AddProjectDialog :open="adding" @close="adding = false" />
  </SettingsPage>
</template>

<style scoped>
.projects__scroll {
  overflow-x: auto;
}
.projects {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.projects th,
.projects td {
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
.projects tbody th {
  font-weight: 700;
  overflow-wrap: anywhere;
}
.projects__branch {
  overflow-wrap: anywhere;
}
.projects__notice {
  display: block;
}
.projects__path {
  overflow-wrap: anywhere;
}
.projects__state[data-state='missing'] {
  color: var(--omo-danger);
}
.projects__state[data-state='disabled'] {
  color: var(--omo-muted);
}
</style>
