import { h } from 'vue'
import { mountDialog } from '../../components/ui/mountDialog'
import FollowUpDialog from './FollowUpDialog.vue'
import { busyReason, registerTicketAction, type TicketAction } from './actions'
import { wasExported } from './exportState'

export const followUpAction: TicketAction = {
  id: 'follow-up',
  label: 'Send follow-up task…',
  applies: (t) => t.column === 'review' && (t.review_stage === 'awaiting_merge' || t.review_stage === 'human') && wasExported(t),
  unavailable: (t, ctx) => ctx.busy ? busyReason(t.number) : undefined,
  describe: (t) => `Sends a task to its ${t.type === 'story' ? 'product manager' : 'developer'} or reviewer. The review policy decides whether you review the changes before omo re-exports them.`,
  run: (t, ctx) => new Promise((resolve) => {
    let outcome = 'cancelled'
    const close = mountDialog(
      (cancel) => h(FollowUpDialog, {
        ticket: t,
        onClose: cancel,
        onSent: (result: 'applied' | 'unknown') => {
          outcome = result
          close()
        },
      }),
      ctx.dialogs,
      () => resolve(outcome),
    )
  }),
}

let unregister: (() => void) | null = null

export function registerFollowUpAction(): void {
  if (unregister === null) unregister = registerTicketAction(followUpAction)
}
