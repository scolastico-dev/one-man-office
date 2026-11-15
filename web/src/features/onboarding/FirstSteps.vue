<script setup lang="ts">
// The first steps shown on a board without any ticket: add the Superpowers
// skill set (recommended, optional), add a project, check the agent setup,
// create a first ticket. Each step ticks itself off from the live data and
// has a button that does it; a refusal shows in its step.
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { failureText } from '../../api/lostAnswer'
import { getSkillSets } from '../../api/endpoints'
import type { ApiEvent, SettingsChangedData } from '../../api/types'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { liveRead } from '../../store/liveRead'
import { hasSuperpowers } from '../skillsets/superpowers'
import AddProjectDialog from '../projects/AddProjectDialog.vue'
import { useSettingsDialogs } from '../topbar/settingsDialogs'
import { useAgentSetup } from './agentSetup'
import FirstStepsPanel, { type FirstStep } from './FirstStepsPanel.vue'
import { firstSteps } from './steps'

const store = useBoard()
const setup = useAgentSetup()
const dialogs = useSettingsDialogs()
const session = useSession()
const say = useAnnouncer()
const nav = { router: useRouter(), client: session.client }

/** Whether a skill set has the Superpowers repository; null until read (a failed read leaves the step open). */
const superpowers = ref<boolean | null>(null)
const skills = liveRead({
  store,
  read: async () => (await getSkillSets(session.client)).data,
  apply: (r) => (superpowers.value = hasSuperpowers(r.skill_sets)),
  watches: (ev: ApiEvent) => ev.kind === 'skillset.changed' || (ev.kind === 'settings.changed' && (ev.data as Partial<SettingsChangedData> | null)?.section === 'skillsets'),
})

/** The step whose action runs, and the refusal each step got. */
const running = ref<string | null>(null)
const errors = reactive(new Map<string, string>())

const steps = computed(() => firstSteps())
const view = computed<FirstStep[]>(() => {
  const ctx = {
    projects: store.state.projects.size,
    tickets: store.state.tickets.size,
    agentsReady: setup.ready.value,
    ceoEnabled: store.state.general?.ceo.enabled ?? false,
    superpowers: superpowers.value,
  }
  return steps.value.map((s) => ({
    id: s.id,
    title: s.title,
    detail: s.text,
    done: s.done(ctx),
    action: s.action.label,
    secondary: s.secondary && (s.secondary.visible?.(ctx) ?? true) ? s.secondary.label : undefined,
    optional: s.optional === true,
    busy: running.value === s.id,
    error: errors.get(s.id),
  }))
})

async function run(id: string) {
  const step = steps.value.find((s) => s.id === id)
  if (!step || running.value !== null) return
  errors.delete(id)
  const p = step.action.run(nav)
  if (!p) return
  running.value = id
  try {
    await p
    void skills.reload()
  } catch (e) {
    const text = failureText(e, { lost: 'No answer from omo; the step shows what omo did.', refused: 'omo refused it.' })
    errors.set(id, text)
    say.assertive(text)
    void skills.reload()
  } finally {
    running.value = null
  }
}
function runSecondary(id: string) {
  steps.value.find((s) => s.id === id)?.secondary?.run(nav)
}
</script>

<template>
  <FirstStepsPanel :steps="view" footer="This list disappears once the board has a ticket." @action="run" @secondary="runSecondary" />
  <AddProjectDialog :open="dialogs.state.open === 'addProject'" @close="dialogs.close()" />
</template>
