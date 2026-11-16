<script setup lang="ts">
// Confirms cancelling a ticket: what happens to its agent, its branch and its
// worktree, and for a story which of its tasks are cancelled with it and whose
// merged work goes when the branch is deleted. Nothing is sent before.
import { computed, ref, shallowRef, useId, watch } from 'vue'
import { listTickets } from '../../api/endpoints'
import type { TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { visibleText } from '../../components/ui/visibleText'
import { COLUMN_TITLES } from '../board/model'
import { withoutProject } from './fieldRules'

const props = withDefaults(
  defineProps<{
    open: boolean
    ticket: TicketSummary
    /** The cancel was sent and has no answer yet. */
    busy?: boolean
    /** Why omo refused to cancel it. */
    error?: string
  }>(),
  { busy: false, error: '' },
)
const emit = defineEmits<{ close: []; confirm: [deleteBranch: boolean] }>()

const store = useBoard()
const session = useSession()
const name = useId()

const choice = ref<'keep' | 'delete'>('keep')
/** The story's tasks as omo listed them; until then the ones the board has loaded. */
const listed = shallowRef<TicketSummary[] | null>(null)
let loadToken = 0

const story = computed(() => props.ticket.type === 'story')
const tasks = computed(() => listed.value ?? store.tasksOf(props.ticket.number))
const openTasks = computed(() => tasks.value.filter((t) => t.column !== 'done'))
const merged = computed(() => tasks.value.filter((t) => t.column === 'done' && t.resolution === 'completed'))
const hasBranch = computed(() => props.ticket.projects.some((p) => p.branch !== ''))
const agents = computed(() => props.ticket.agents.filter((a) => a.state !== 'ended'))
/** A task without a project has no branch; its working folder stays. */
const noProject = computed(() => withoutProject(props.ticket))

async function loadTasks() {
  const my = ++loadToken
  try {
    const res = await listTickets(session.client, { story: props.ticket.number, done_limit: 500 })
    if (my === loadToken) listed.value = res.data.tickets.filter((t) => t.story === props.ticket.number)
  } catch {
    // The board's own list stays shown.
  }
}

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return
    choice.value = 'keep'
    listed.value = null
    if (story.value) void loadTasks()
  },
  { immediate: true },
)

function submit() {
  if (props.busy) return
  emit('confirm', choice.value === 'delete')
}
</script>

<template>
  <UiDialog :open="open" :title="`Cancel #${ticket.number}?`" :eyebrow="ticket.title" @close="emit('close')">
    <form id="cancel-form" class="cancel" novalidate data-cancel-dialog @submit.prevent="submit">
      <p class="cancel__text">
        <template v-if="agents.length > 0">The agent stops and the ticket is closed as cancelled.</template>
        <template v-else>The ticket is closed as cancelled. No agent is working on it.</template>
      </p>
      <template v-if="story">
        <template v-if="openTasks.length > 0">
          <p class="cancel__text">These tasks are cancelled too:</p>
          <ul class="cancel__list" data-cancel-tasks>
            <li v-for="t in openTasks" :key="t.number">#{{ t.number }} {{ t.title }} ({{ COLUMN_TITLES[t.column] }})</li>
          </ul>
        </template>
        <p v-else class="cancel__text cancel__muted">It has no open tasks.</p>
      </template>
      <p v-if="noProject" class="cancel__text" data-cancel-effect>
        It has no project and no branch.<template v-if="ticket.workdir !== ''"> Its working folder <code>{{ visibleText(ticket.workdir) }}</code> stays until you remove it.</template>
      </p>
      <template v-else>
        <fieldset class="cancel__choice">
          <legend class="cancel__legend">The branch{{ hasBranch ? '' : ' (none exists yet)' }}</legend>
          <label class="cancel__option">
            <input v-model="choice" type="radio" :name="name" value="keep" />
            <span>Keep the branch</span>
          </label>
          <label class="cancel__option">
            <input v-model="choice" type="radio" :name="name" value="delete" />
            <span>Delete the branch</span>
          </label>
        </fieldset>
        <p v-if="choice === 'keep'" class="cancel__text" data-cancel-effect>
          The branch and its worktree stay, so the work can still be picked up by hand.
        </p>
        <template v-else>
          <p class="cancel__text" data-cancel-effect>
            The branch and its worktree are removed. If the worktree has uncommitted changes, omo keeps the folder and the branch and lists the files in a comment on the ticket.
          </p>
          <template v-if="story && merged.length > 0">
            <p class="cancel__text">Deleting the branch discards the work of these merged tasks:</p>
            <ul class="cancel__list" data-cancel-merged>
              <li v-for="t in merged" :key="t.number">#{{ t.number }} {{ t.title }}</li>
            </ul>
          </template>
        </template>
      </template>
      <p v-if="error" class="cancel__error" role="alert" data-action-error>{{ error }}</p>
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Keep the ticket</UiButton>
      <UiButton type="submit" form="cancel-form" variant="danger" :disabled="busy" data-primary>
        {{ busy ? 'Cancelling…' : 'Cancel the ticket' }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.cancel {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.cancel__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.cancel__muted {
  color: var(--omo-muted);
}
.cancel__list {
  margin: 0;
  padding-left: calc(var(--omo-space) * 5);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.cancel__choice {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
}
.cancel__legend {
  padding: 0 var(--omo-space);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.cancel__option {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.cancel__option input {
  accent-color: var(--omo-accent);
}
.cancel__error {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
