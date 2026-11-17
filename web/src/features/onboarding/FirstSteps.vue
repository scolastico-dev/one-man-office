<script setup lang="ts">
// The first steps shown on a board without any ticket: add the Superpowers
// skill set (recommended, optional), add a project, check the agent setup
// (or, when omo found no agent program, detect one), register omo in
// Antigravity (recommended; only with an Antigravity profile while MCP is
// on), create a first ticket. Each step ticks itself off from the live data
// and has a button that does it; a refusal shows in its step. The user can
// dismiss a step or hide the list (remembered in this browser).
import { computed, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { failureText } from '../../api/lostAnswer'
import { getSkillSets, listMCPClients } from '../../api/endpoints'
import type { ApiEvent, SettingsChangedData } from '../../api/types'
import { concernsClients } from '../../store/mcpEvents'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { liveRead } from '../../store/liveRead'
import { hasSuperpowers } from '../skillsets/superpowers'
import AddProjectDialog from '../projects/AddProjectDialog.vue'
import { useSettingsDialogs } from '../topbar/settingsDialogs'
import { useAgentSetup } from './agentSetup'
import { useDismissed } from './dismissed'
import FirstStepsPanel, { type FirstStep } from './FirstStepsPanel.vue'
import { firstSteps, forContext, type StepContext } from './steps'

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

/** Whether a profile runs Antigravity while omo's MCP endpoint is on: then omo's entry in its configuration matters. */
const agyNeedsEntry = computed(() => (store.state.general?.mcp.enabled ?? false) && (setup.report.value?.profiles.some((p) => p.cli === 'agy') ?? false))
/** Whether omo's entry is in an Antigravity configuration; null until read (a failed read leaves the step open). */
const agyRegistered = ref<boolean | null>(null)
const agy = liveRead({
  store,
  read: async () => (agyNeedsEntry.value ? (await listMCPClients(session.client)).data : null),
  apply: (r) => (agyRegistered.value = r === null ? null : (r ?? []).some((c) => c.target.cli === 'agy' && c.state === 'installed')),
  watches: concernsClients,
})
watch(agyNeedsEntry, (need) => need && void agy.reload())

const dismissed = useDismissed()

/** The step whose action runs, and the refusal each step got. */
const running = ref<string | null>(null)
const errors = reactive(new Map<string, string>())

const steps = computed(() => firstSteps())
const ctx = computed<StepContext>(() => ({
  projects: store.state.projects.size,
  tickets: store.state.tickets.size,
  agentsReady: setup.ready.value,
  hasProfiles: setup.report.value === null ? null : setup.report.value.hasProfiles,
  ceoEnabled: store.state.general?.ceo.enabled ?? false,
  ceoTicketsWait: store.state.general?.ceo.tickets_need_user_approval ?? false,
  superpowers: superpowers.value,
  agyNeedsEntry: agyNeedsEntry.value,
  agyRegistered: agyRegistered.value,
}))
const view = computed<FirstStep[]>(() => {
  const c = ctx.value
  return steps.value
    .filter((s) => (s.shown?.(c) ?? true) && !dismissed.state.steps.includes(s.id))
    .map((s) => ({
      id: s.id,
      title: s.title,
      detail: forContext(s.text, c),
      done: s.done(c),
      action: forContext(s.action.label, c),
      secondary: s.secondary && (s.secondary.visible?.(c) ?? true) ? s.secondary.label : undefined,
      optional: s.optional === true,
      busy: running.value === s.id,
      error: errors.get(s.id),
    }))
})

async function run(id: string) {
  const step = steps.value.find((s) => s.id === id)
  if (!step || running.value !== null) return
  errors.delete(id)
  const p = step.action.run(nav, ctx.value)
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
  <FirstStepsPanel
    v-if="!dismissed.state.hidden"
    :steps="view"
    footer="This list disappears once the board has a ticket."
    @action="run"
    @secondary="runSecondary"
    @dismiss="dismissed.dismiss"
    @hide="dismissed.hideAll()"
  />
  <AddProjectDialog :open="dialogs.state.open === 'addProject'" @close="dialogs.close()" />
</template>
