<script setup lang="ts">
// The gear menu of the top bar; its entries come from `settingsMenu()`.
import { computed } from 'vue'
import UiMenu, { type MenuItem } from '../../components/ui/UiMenu.vue'
import AgentSetupDialog from '../onboarding/AgentSetupDialog.vue'
import ProjectsDialog from '../projects/ProjectsDialog.vue'
import { settingsMenu, useSettingsDialogs } from './settingsMenu'
import ThemeDialog from './ThemeDialog.vue'

const entries = settingsMenu()
const dialogs = useSettingsDialogs()
const items = computed<MenuItem[]>(() => entries.map((e) => ({ id: e.id, label: e.label })))

function select(id: string) {
  entries.find((e) => e.id === id)?.open()
}
</script>

<template>
  <UiMenu label="Settings" :items="items" icon-only variant="ghost" align="end" @select="select">
    <template #icon>
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
        <path
          d="M13.24 6.68 15.09 6.73 15.09 9.27 13.24 9.32 12.99 10.07 12.64 10.77 13.91 12.12 12.12 13.91 10.77 12.64 10.07 12.99 9.32 13.24 9.27 15.09 6.73 15.09 6.68 13.24 5.93 12.99 5.23 12.64 3.88 13.91 2.09 12.12 3.36 10.77 3.01 10.07 2.76 9.32 .91 9.27 .91 6.73 2.76 6.68 3.01 5.93 3.36 5.23 2.09 3.88 3.88 2.09 5.23 3.36 5.93 3.01 6.68 2.76 6.73 .91 9.27 .91 9.32 2.76 10.07 3.01 10.77 3.36 12.12 2.09 13.91 3.88 12.64 5.23 12.99 5.93Z"
          stroke-linejoin="round"
        />
        <circle cx="8" cy="8" r="2.3" />
      </svg>
    </template>
  </UiMenu>
  <!-- Outside the top bar, so the dialogs' own header and footer are not read as the page's landmarks. -->
  <Teleport to="body">
    <ProjectsDialog :open="dialogs.state.open === 'projects'" @close="dialogs.close()" />
    <ThemeDialog :open="dialogs.state.open === 'theme'" @close="dialogs.close()" />
    <AgentSetupDialog :open="dialogs.state.open === 'agents'" @close="dialogs.close()" />
  </Teleport>
</template>
