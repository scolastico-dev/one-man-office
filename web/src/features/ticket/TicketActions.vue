<script setup lang="ts">
// The actions that apply to the ticket now, each saying what it does or why it
// cannot run; a ticket that needs attention shows why first. Approving a
// review, rejecting and cancelling ask in a dialog before anything is sent.
// Answers are shown here: toasts would sit under the panel.
import { computed, ref, shallowRef, useId, watch } from 'vue'
import { approveTicket, cancelTicket } from '../../api/endpoints'
import { failureText, isLostAnswer } from '../../api/lostAnswer'
import type { TicketSummary } from '../../api/types'
import { dragTargets, type RuleContext } from '../../board/moveRules'
import UiButton from '../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import RejectDialog from '../board/dialogs/RejectDialog.vue'
import { captionFor } from '../../board/badges'
import ApproveDialog from './ApproveDialog.vue'
import CancelDialog from './CancelDialog.vue'
import { labelOf, ticketActions, type ActionContext, type ActionDialog, type TicketAction } from './actions'

const props = defineProps<{ ticket: TicketSummary }>()

const store = useBoard()
const session = useSession()
const say = useAnnouncer()
const hintId = useId()

const STEP_TEXT: Record<string, string> = {
  start: 'Starting work on it failed.',
  agent: 'Its agent keeps failing.',
  merge: 'The merge is blocked.',
}

const rules: RuleContext = {
  project: (id) => store.state.projects.get(id),
  storyHasLiveAgent: (story) => store.tasksOf(story).some((t) => t.agents.length > 0),
}

const busy = computed(() => {
  const t = props.ticket
  return t.busy === true || (t.type === 'story' && store.tasksOf(t.number).some((x) => x.busy === true))
})

const dialog = ref<ActionDialog | null>(null)
const dialogBusy = ref(false)
const dialogError = ref('')
/** The action whose request is under way. */
const running = ref<string | null>(null)
const error = shallowRef<{ action: string; text: string } | null>(null)

const ctx = computed<ActionContext>(() => ({
  targets: dragTargets(props.ticket, rules),
  busy: busy.value,
  client: session.client,
  store,
  ask: (d) => openDialog(d),
}))

const actions = computed(() =>
  ticketActions(props.ticket, ctx.value).map((a) => ({
    a,
    label: labelOf(a, props.ticket),
    why: a.unavailable?.(props.ticket, ctx.value),
    hint: a.describe?.(props.ticket, ctx.value) ?? '',
  })),
)

const attention = computed(() => {
  const t = props.ticket
  if (!t.failed_step && !t.attention) return null
  const step = t.failed_step ? STEP_TEXT[t.failed_step] ?? `The step ${t.failed_step} failed.` : ''
  return { step, reason: t.attention }
})

const caption = computed(() => captionFor(props.ticket))

// A change of state ends an error that was about the old one.
watch(
  () => [props.ticket.column, props.ticket.review_stage, props.ticket.failed_step, props.ticket.resolution],
  () => {
    error.value = null
  },
)

function message(e: unknown, what: string): string {
  return failureText(e, {
    lost: `No answer from omo, so ${what} may have happened. The panel shows what omo did.`,
    refused: 'omo refused it.',
  })
}

function openDialog(d: ActionDialog) {
  dialogBusy.value = false
  dialogError.value = ''
  dialog.value = d
}

function closeDialog() {
  dialog.value = null
  dialogBusy.value = false
  dialogError.value = ''
}

async function run(a: TicketAction, label: string) {
  if (running.value !== null) return
  error.value = null
  const p = a.run(props.ticket, ctx.value)
  if (!p) return
  running.value = a.id
  try {
    const outcome = await p
    say.polite(outcome === 'unknown' ? `No answer for ${label} on #${props.ticket.number}. The panel shows what omo did.` : `${label}: done for #${props.ticket.number}.`)
  } catch (e) {
    const text = message(e, `${label.toLowerCase()} on #${props.ticket.number}`)
    error.value = { action: a.id, text }
    say.assertive(`${label} refused: ${text}`)
  } finally {
    running.value = null
  }
}

/**
 * Sends the dialog's request. The dialog closes once omo has accepted it; a
 * refusal is shown inside the dialog, which keeps what was typed.
 */
async function confirm(send: () => Promise<unknown>, done: string) {
  if (dialogBusy.value) return
  const d = dialog.value
  dialogBusy.value = true
  dialogError.value = ''
  const n = props.ticket.number
  const requestId = store.pending.get(n)?.requestId
  // A move shows at once; the dialog closes as soon as omo accepted it.
  const stop = watch(
    () => {
      const p = store.pending.get(n)
      return p !== undefined && p.requestId !== requestId && p.waitFor !== null
    },
    (accepted) => {
      if (accepted && dialog.value === d) closeDialog()
    },
  )
  try {
    const outcome = await send()
    if (dialog.value === d) closeDialog()
    say.polite(outcome === 'unknown' ? `No answer from omo for #${n}. The panel shows what omo did.` : done)
  } catch (e) {
    const text = message(e, `the change to #${n}`)
    say.assertive(text)
    if (dialog.value === d) {
      dialogBusy.value = false
      dialogError.value = text
    }
  } finally {
    stop()
  }
}

/**
 * Approves with an explicit way (`export` or `merge`). Unlike the plain
 * approval it is not a move, so the board shows the new stage when the stream
 * delivers it; a lost answer ends the dialog and says the panel shows the truth.
 */
async function approveWith(opts: { export?: boolean; merge?: boolean }): Promise<'applied' | 'unknown'> {
  try {
    await approveTicket(session.client, props.ticket.number, opts)
    return 'applied'
  } catch (e) {
    if (isLostAnswer(e)) return 'unknown'
    throw e
  }
}

function confirmApprove(opts?: { export?: boolean; merge?: boolean }) {
  const n = props.ticket.number
  if (opts === undefined) void confirm(() => store.requestMove(n, 'done', null), `#${n} approved.`)
  else void confirm(() => approveWith(opts), opts.export ? `#${n} approved for export.` : `#${n} approved, merging locally.`)
}

function confirmReject(comment: string) {
  void confirm(() => store.requestMove(props.ticket.number, 'in_progress', null, { reject: { comment } }), `#${props.ticket.number} rejected.`)
}

function confirmCancel(deleteBranch: boolean) {
  void confirm(() => cancelTicket(session.client, props.ticket.number, deleteBranch), `#${props.ticket.number} cancelled.`)
}
</script>

<template>
  <div class="actions" data-ticket-actions>
    <div v-if="attention" class="actions__attention" role="status" data-attention>
      <p class="actions__attention-head">Needs attention</p>
      <p v-if="attention.step" class="actions__line">{{ attention.step }}</p>
      <p v-if="attention.reason" class="actions__line">{{ attention.reason }}</p>
    </div>
    <p v-if="busy" class="actions__busy" data-busy>
      omo is working on this{{ caption ? `: ${caption}` : '.' }}
    </p>
    <ul v-if="actions.length > 0" class="actions__list" aria-label="Actions">
      <li v-for="x in actions" :key="x.a.id" class="actions__item">
        <UiButton
          :variant="x.a.tone ?? 'default'"
          size="sm"
          :disabled="x.why !== undefined || running !== null"
          :title="x.why ?? x.hint"
          :aria-describedby="`${hintId}-${x.a.id}`"
          :data-action="x.a.id"
          @click="run(x.a, x.label)"
        >
          {{ running === x.a.id ? `${x.label.replace(/…$/, '')}…` : x.label }}
        </UiButton>
        <span :id="`${hintId}-${x.a.id}`" class="sr-only">{{ x.hint }}</span>
        <p v-if="x.why" class="actions__why" :data-action-why="x.a.id">{{ x.why }}</p>
        <p v-if="error?.action === x.a.id" class="actions__error" role="alert" data-action-error>{{ error.text }}</p>
      </li>
    </ul>
    <ApproveDialog
      v-if="dialog === 'approve' || dialog === 'export'"
      open
      :mode="dialog === 'export' ? 'export' : 'default'"
      :ticket="ticket"
      :busy="dialogBusy"
      :error="dialogError"
      @close="closeDialog"
      @confirm="confirmApprove"
    />
    <RejectDialog
      v-if="dialog === 'reject'"
      open
      :ticket="ticket"
      :busy="dialogBusy"
      :error="dialogError"
      @close="closeDialog"
      @confirm="confirmReject"
    />
    <CancelDialog
      v-if="dialog === 'cancel'"
      open
      :ticket="ticket"
      :busy="dialogBusy"
      :error="dialogError"
      @close="closeDialog"
      @confirm="confirmCancel"
    />
  </div>
</template>

<style scoped>
.actions {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
.actions__attention {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  border-left-width: var(--omo-edge);
  background: var(--omo-danger-soft);
}
.actions__attention-head {
  margin: 0;
  color: var(--omo-danger);
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.actions__line,
.actions__busy {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.actions__busy {
  color: var(--omo-warn);
}
.actions__list {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.actions__item {
  display: grid;
  gap: var(--omo-space);
  max-width: 100%;
}
.actions__why,
.actions__error {
  max-width: 40ch;
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.actions__error {
  color: var(--omo-danger);
}
</style>
