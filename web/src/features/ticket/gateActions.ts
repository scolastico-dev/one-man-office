// The user's two ways out of a plugin gate, as actions of the ticket panel:
// Continue anyway… (every plugin that has not passed counts as having given
// its go-ahead) and Cancel move… (the held move is not made). Both ask first,
// over the panel; the server decides whether they can run and its refusal is
// shown by the actions row. The plugins' own choices are in TicketGate.vue.
import { h } from 'vue'
import { cancelGateMove, overrideGate } from '../../api/endpoints'
import { isLostAnswer } from '../../api/lostAnswer'
import type { TicketGates, TicketSummary } from '../../api/types'
import { mountDialog, type DialogScope } from '../../components/ui/mountDialog'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { COLUMN_TITLES } from '../board/model'
import { registerTicketAction, type TicketAction } from './actions'

const targetTitle = (g: TicketGates) => COLUMN_TITLES[g.to]

/** What Continue anyway does, in one sentence. */
export function overrideSentence(t: TicketSummary): string {
  const g = t.gate
  const to = g === null ? 'the next column' : targetTitle(g)
  return `Every plugin that has not given its go-ahead counts as having given it, and #${t.number} goes on to ${to}.`
}

/** What Cancel move does, in one sentence: the result the board defines for the held move. */
export function cancelSentence(t: TicketSummary): string {
  const g = t.gate
  if (g === null) return 'The held move is not made.'
  return g.move === 'start'
    ? `The start is cancelled and #${t.number} goes back to ${COLUMN_TITLES.planning}.`
    : `The move to ${targetTitle(g)} is not made; #${t.number} stays in ${COLUMN_TITLES.planning}.`
}

/** Asks first; resolves true when the user agreed, false when not or when the panel went away. Nothing is sent before. */
function confirm(title: string, text: string, label: string, dialogs: DialogScope, danger: boolean): Promise<boolean> {
  return new Promise<boolean>((done) => {
    let agreed = false
    const close = mountDialog(
      (cancel) =>
        h(
          UiDialog,
          { open: true, title, onClose: cancel, 'data-gate-dialog': '' },
          {
            default: () => [h('p', { class: 'm-0' }, text)],
            actions: () => [
              h(UiButton, { onClick: cancel, 'data-gate-cancel': '' }, () => 'Back'),
              h(
                UiButton,
                {
                  variant: danger ? 'danger' : 'primary',
                  'data-primary': '',
                  onClick: () => {
                    agreed = true
                    close()
                  },
                  'data-gate-confirm': '',
                },
                () => label,
              ),
            ],
          },
        ),
      dialogs,
      () => done(agreed),
    )
  })
}

async function send(p: Promise<unknown>): Promise<'unknown' | undefined> {
  try {
    await p
  } catch (e) {
    // No answer: the panel shows what omo did.
    if (isLostAnswer(e)) return 'unknown'
    throw e
  }
  return undefined
}

export const gateOverrideAction: TicketAction = {
  id: 'gate-override',
  label: 'Continue anyway…',
  applies: (t) => t.gate !== null,
  explain: true,
  describe: overrideSentence,
  run: async (t, ctx) => {
    if (!(await confirm(`Continue #${t.number} anyway?`, overrideSentence(t), 'Continue anyway', ctx.dialogs, false))) return 'cancelled'
    return send(overrideGate(ctx.client, t.number))
  },
}

export const gateCancelAction: TicketAction = {
  id: 'gate-cancel',
  label: 'Cancel move…',
  tone: 'danger',
  applies: (t) => t.gate !== null,
  explain: true,
  describe: cancelSentence,
  run: async (t, ctx) => {
    if (!(await confirm(`Cancel the move of #${t.number}?`, cancelSentence(t), 'Cancel move', ctx.dialogs, true))) return 'cancelled'
    return send(cancelGateMove(ctx.client, t.number))
  },
}

let unregister: (() => void) | null = null

/** Adds the two actions to every ticket panel; calling it again changes nothing. */
export function registerGateActions(): void {
  if (unregister !== null) return
  const off = [registerTicketAction(gateOverrideAction), registerTicketAction(gateCancelAction)]
  unregister = () => off.forEach((f) => f())
}
