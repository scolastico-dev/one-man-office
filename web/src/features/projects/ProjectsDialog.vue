<script setup lang="ts">
// Read-only list of the known projects, with the button that adds one.
import { computed, ref } from 'vue'
import type { Project } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useBoard } from '../../store'
import AddProjectDialog from './AddProjectDialog.vue'
import { projectState } from './projectState'

defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const store = useBoard()
const adding = ref(false)

const projects = computed<Project[]>(() =>
  [...store.state.projects.values()].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id)),
)
</script>

<template>
  <UiDialog :open="open" title="Projects" eyebrow="settings" wide @close="emit('close')">
    <p v-if="projects.length === 0" class="m-0 text-muted">No project yet. A project is a local git repository omo may work in.</p>
    <div v-else class="projects__scroll">
      <table class="projects">
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Path</th>
            <th scope="col">State</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in projects" :key="p.id" :data-project="p.id">
            <th scope="row">{{ p.name }}</th>
            <td class="projects__path">{{ p.path }}</td>
            <td>
              <span class="projects__state" :data-state="projectState(p)">{{ projectState(p) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <template #actions>
      <UiButton @click="emit('close')">Close</UiButton>
      <UiButton variant="primary" @click="adding = true">Add project</UiButton>
    </template>
    <AddProjectDialog :open="adding" @close="adding = false" />
  </UiDialog>
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
