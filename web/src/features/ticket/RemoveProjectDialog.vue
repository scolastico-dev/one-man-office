<script setup lang="ts">
// Confirms taking a project off a ticket: names what omo deletes (the
// worktree and the ticket's own branch there) and, when the server refuses
// because that would delete work, lists the commits and files and asks a
// second time before it sends the removal with discard.
import { computed, ref, watch } from 'vue'
import { removeTicketProject } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { ProjectWork, TicketProject, TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { workOf } from './removeProject'

const props = defineProps<{ open: boolean; ticket: TicketSummary; project: TicketProject }>()
const emit = defineEmits<{ close: [] }>()

const session = useSession()
const store = useBoard()

const busy = ref(false)
const error = ref('')
/** Set once the server said the removal would delete this work. */
const work = ref<ProjectWork | null>(null)

const name = computed(() => store.state.projects.get(props.project.project)?.name ?? props.project.project)
const waiting = computed(() => props.ticket.agents.filter((a) => a.state === 'waiting' && a.project === props.project.project))

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return
    busy.value = false
    error.value = ''
    work.value = null
  },
  { immediate: true },
)

async function submit() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await removeTicketProject(session.client, props.ticket.number, props.project.project, work.value !== null)
    emit('close')
  } catch (err) {
    const w = workOf(err)
    if (w && work.value === null) {
      work.value = w
    } else {
      error.value = failureText(err, {
        lost: `No answer from omo, so removing ${name.value} may have happened. The ticket shows what omo did.`,
        refused: 'omo refused it.',
      })
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog :open="open" :title="`Remove ${name} from #${ticket.number}?`" :eyebrow="ticket.title" @close="emit('close')">
    <form id="remove-project-form" class="remove" novalidate data-remove-project-dialog @submit.prevent="submit">
      <template v-if="work === null">
        <p class="remove__text">The project leaves the ticket. omo deletes what it made for the ticket there:</p>
        <ul class="remove__list" data-remove-effects>
          <li v-if="project.worktree !== ''">the worktree <code>{{ visibleText(project.worktree) }}</code></li>
          <li v-if="project.branch !== ''">
            the branch <code>{{ visibleText(project.branch) }}</code>, if it is this ticket's own; any other branch is kept
          </li>
        </ul>
        <p class="remove__text" data-remove-agents>
          Agents of the ticket that only wait there are ended<template v-if="waiting.length > 0"> ({{ waiting.map((a) => a.name).join(', ') }})</template>; while
          one works there, omo refuses.
        </p>
        <p class="remove__text remove__muted">If that would delete commits or uncommitted changes, omo lists them and asks again.</p>
      </template>
      <template v-else>
        <p class="remove__text" data-remove-work>Removing {{ name }} deletes this work too:</p>
        <template v-if="work.commits.length > 0">
          <p class="remove__text">
            Commits of <code>{{ visibleText(work.branch) }}</code> that <code>{{ visibleText(work.base) }}</code> does not contain:
          </p>
          <ul class="remove__list" data-remove-commits>
            <li v-for="c in work.commits" :key="c">{{ visibleText(c) }}</li>
          </ul>
        </template>
        <template v-if="work.files.length > 0">
          <p class="remove__text">Uncommitted changes in the worktree:</p>
          <ul class="remove__list" data-remove-files>
            <li v-for="f in work.files" :key="f">{{ visibleText(f) }}</li>
          </ul>
        </template>
      </template>
      <p v-if="error" class="remove__error" role="alert" data-action-error>{{ error }}</p>
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Keep the project</UiButton>
      <UiButton type="submit" form="remove-project-form" variant="danger" :disabled="busy" data-primary>
        {{ busy ? 'Removing…' : work === null ? 'Remove from ticket' : 'Delete the work and remove' }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.remove {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.remove__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.remove__muted {
  color: var(--omo-muted);
}
.remove__list {
  margin: 0;
  padding-left: calc(var(--omo-space) * 5);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.remove__error {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
