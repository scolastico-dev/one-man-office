// The Start anyway… action: starts a ticket that waits for usage on its own
// model although the model is near its limit. The server decides whether it
// can be done; its refusal is shown in the panel by the actions row.
import { h } from 'vue'
import { startAnyway } from '../../api/endpoints'
import { isLostAnswer } from '../../api/lostAnswer'
import type { TicketSummary } from '../../api/types'
import { mountDialog, type DialogScope } from '../../components/ui/mountDialog'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { registerTicketAction, type TicketAction } from './actions'

/** Whether the ticket waits for usage at all. */
const waitsForUsage = (t: TicketSummary) => t.start_blocked === 'usage'

/** Why the server would refuse, in words; undefined when it would not. */
export function startAnywayUnavailable(t: TicketSummary): string | undefined {
  return t.model === '' ? `#${t.number} has no model of its own; it waits until a model of its role has room.` : undefined
}

const sentence = (t: TicketSummary) => `Starts it on ${visibleText(t.model)} although the model is near its usage limit.`

/** Asks first, over the panel; resolves true when the user agreed, false when not or when the panel went away. Nothing is sent before. */
function confirmStart(t: TicketSummary, dialogs: DialogScope): Promise<boolean> {
  return new Promise<boolean>((done) => {
    let agreed = false
    const close = mountDialog(
      (cancel) =>
        h(
          UiDialog,
          { open: true, title: `Start #${t.number} anyway?`, eyebrow: `#${t.number}`, onClose: cancel, 'data-start-anyway-dialog': '' },
          {
            default: () => [
              h('p', { class: 'm-0' }, sentence(t)),
              h('p', { class: 'm-0 mt-2 text-sm' }, 'Its agent may be stopped again when the model reaches its hard limit.'),
            ],
            actions: () => [
              h(UiButton, { onClick: cancel, 'data-start-anyway-cancel': '' }, () => 'Cancel'),
              h(
                UiButton,
                {
                  variant: 'primary',
                  'data-primary': '',
                  onClick: () => {
                    agreed = true
                    close()
                  },
                  'data-start-anyway-confirm': '',
                },
                () => 'Start anyway',
              ),
            ],
          },
        ),
      dialogs,
      () => done(agreed),
    )
  })
}

export const startAnywayAction: TicketAction = {
  id: 'start-anyway',
  label: 'Start anyway…',
  applies: waitsForUsage,
  unavailable: (t) => startAnywayUnavailable(t),
  describe: (t) => (t.model === '' ? 'Starts it although its model is near its usage limit.' : sentence(t)),
  run: async (t, ctx) => {
    if (!(await confirmStart(t, ctx.dialogs))) return 'cancelled'
    try {
      await startAnyway(ctx.client, t.number)
    } catch (e) {
      // No answer: the panel shows what omo did.
      if (isLostAnswer(e)) return 'unknown'
      throw e
    }
  },
}

let unregister: (() => void) | null = null

/** Adds Start anyway… to every ticket panel; calling it again changes nothing. */
export function registerStartAnywayAction(): void {
  if (unregister === null) unregister = registerTicketAction(startAnywayAction)
}
