<script setup lang="ts">
// The first steps shown on a board without any ticket: add a project, check
// the agent setup, create a first ticket. Each step ticks itself off from the
// live data and has a button that does it.
import { computed } from 'vue'
import { useBoard } from '../../store'
import AddProjectDialog from '../projects/AddProjectDialog.vue'
import { useSettingsDialogs } from '../topbar/settingsMenu'
import { useAgentSetup } from './agentSetup'
import FirstStepsPanel, { type FirstStep } from './FirstStepsPanel.vue'
import { firstSteps } from './steps'

const store = useBoard()
const setup = useAgentSetup()
const dialogs = useSettingsDialogs()

const steps = computed(() => firstSteps())
const view = computed<FirstStep[]>(() => {
  const ctx = {
    projects: store.state.projects.size,
    tickets: store.state.tickets.size,
    agentsReady: setup.ready.value,
    ceoEnabled: store.state.general?.ceo.enabled ?? false,
  }
  return steps.value.map((s) => ({
    id: s.id,
    title: s.title,
    detail: s.text,
    done: s.done(ctx),
    action: s.action.label,
    secondary: s.secondary && (s.secondary.visible?.(ctx) ?? true) ? s.secondary.label : undefined,
  }))
})

function run(id: string) {
  steps.value.find((s) => s.id === id)?.action.run()
}
function runSecondary(id: string) {
  steps.value.find((s) => s.id === id)?.secondary?.run()
}
</script>

<template>
  <FirstStepsPanel :steps="view" footer="This list disappears once the board has a ticket." @action="run" @secondary="runSecondary" />
  <AddProjectDialog :open="dialogs.state.open === 'addProject'" @close="dialogs.close()" />
</template>
