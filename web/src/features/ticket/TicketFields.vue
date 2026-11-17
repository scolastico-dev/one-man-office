<script setup lang="ts">
// The fields on the right of the ticket: each shows its value and whether
// and why it can be changed; clicking an editable one changes it in place.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { listModels } from '../../api/endpoints'
import type { Model, TicketSummary } from '../../api/types'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { agentActivity } from './agentActivity'
import { fieldState, withoutProject, type FieldName } from './fieldRules'
import BaseBranchField from './fields/BaseBranchField.vue'
import BlocksField from './fields/BlocksField.vue'
import BranchField from './fields/BranchField.vue'
import DatesField from './fields/DatesField.vue'
import DependenciesField from './fields/DependenciesField.vue'
import MaxParallelField from './fields/MaxParallelField.vue'
import ModelField from './fields/ModelField.vue'
import ProjectsField from './fields/ProjectsField.vue'
import ReviewField from './fields/ReviewField.vue'
import StoryField from './fields/StoryField.vue'
import SupervisorField from './fields/SupervisorField.vue'
import TaskReleaseField from './fields/TaskReleaseField.vue'
import { useGeneral } from './fields/useGeneral'
import WhenApprovedField from './fields/WhenApprovedField.vue'
import WorkdirField from './fields/WorkdirField.vue'

const props = defineProps<{ ticket: TicketSummary }>()

const session = useSession()
const store = useBoard()
const { ceoEnabled } = useGeneral()

const state = (f: FieldName) => fieldState(props.ticket, f, { ceoEnabled: ceoEnabled.value })
const show = (f: FieldName) => state(f).visible

// Every model the server knows (the user may set any of them); without them the field still shows and keeps the current one.
const models = ref<Model[]>([])
let alive = true
async function loadModels() {
  try {
    const { data } = await listModels(session.client)
    if (alive) models.value = data
  } catch {
    if (alive) models.value = []
  }
}
onMounted(() => void loadModels())
onBeforeUnmount(() => (alive = false))
// Profiles can change while the panel is open.
const offSettings = store.onEvent('settings.changed', () => void loadModels())
onBeforeUnmount(offSettings)

/** What the ticket's live agents do now; each reads a comment about every change made to the ticket. */
const activity = computed(() => agentActivity(props.ticket))
const liveAgents = computed(() => props.ticket.agents.filter((a) => a.state !== 'ended').length)
</script>

<template>
  <div class="fields" data-ticket-fields>
    <p v-if="activity && ticket.column !== 'done'" class="fields__note" data-agent-note>
      {{ activity }} Changing a field adds a comment that tells {{ liveAgents === 1 ? 'it' : 'them' }}.
    </p>
    <dl class="fields__list">
      <ProjectsField :ticket="ticket" :state="state('projects')" />
      <StoryField v-if="show('story')" :ticket="ticket" :state="state('story')" />
      <DependenciesField :ticket="ticket" :state="state('depends_on')" />
      <BlocksField :ticket="ticket" />
      <SupervisorField :ticket="ticket" :state="state('supervisor')" />
      <ModelField :ticket="ticket" :state="state('model')" :models="models" />
      <ReviewField :ticket="ticket" :state="state('review')" />
      <WhenApprovedField v-if="show('when_approved')" :ticket="ticket" :state="state('when_approved')" />
      <TaskReleaseField v-if="show('task_release')" :ticket="ticket" :state="state('task_release')" />
      <MaxParallelField v-if="show('max_parallel')" :ticket="ticket" :state="state('max_parallel')" />
      <WorkdirField v-if="withoutProject(ticket)" :ticket="ticket" />
      <BranchField v-else :ticket="ticket" />
      <BaseBranchField v-if="show('base_branch')" :ticket="ticket" :state="state('base_branch')" />
      <DatesField :ticket="ticket" />
    </dl>
  </div>
</template>

<style scoped>
.fields {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.fields__list {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  margin: 0;
}
.fields__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
