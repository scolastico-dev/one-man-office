// The Start anyway… action: starts a ticket that waits for usage on its own
// model although the model is near its limit. The server decides whether it
// can be done; its refusal is shown in the panel by the actions row. A model
// at its hard limit would have its agent stopped again at once: the action is
// then disabled and says so (the usage state comes from useUsage, kept live
// by the ticket panel through useStartAnywayUsage).
import { h } from 'vue'
import { startAnyway } from '../../api/endpoints'
import { isLostAnswer } from '../../api/lostAnswer'
import type { ModelUsageState, TicketSummary } from '../../api/types'
import { mountDialog, type DialogScope } from '../../components/ui/mountDialog'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { useUsage, type Usage } from '../sidebar/useUsage'
import { registerTicketAction, type TicketAction } from './actions'

/** Whether the ticket waits for usage at all. */
const waitsForUsage = (t: TicketSummary) => t.start_blocked === 'usage'

const usageOf = new WeakMap<object, Usage>()

/** Keeps the usage of the session's models current for Start anyway… while the calling component (the ticket panel) is mounted. */
export function useStartAnywayUsage(): void {
  const session = useSession()
  usageOf.set(session.store, useUsage())
}

/** The server's verdict on the ticket's own model, when known. */
function modelState(t: TicketSummary, store: object): ModelUsageState | undefined {
  return usageOf.get(store)?.state.value?.models.find((m) => m.model === t.model)?.state
}

const stoppedAtOnce = (t: TicketSummary) => `${visibleText(t.model)} is at its usage limit: its agent would be stopped again at once.`

/** Why it cannot be started anyway, in words; undefined when it can. `state` is the verdict on the ticket's model. */
export function startAnywayUnavailable(t: TicketSummary, state?: ModelUsageState): string | undefined {
  if (t.model === '') return `#${t.number} has no model of its own; it waits until a model of its role has room.`
  if (state === 'hard') return stoppedAtOnce(t)
  return undefined
}

const sentence = (t: TicketSummary, state?: ModelUsageState) =>
  state === 'hard'
    ? `Starts it on ${visibleText(t.model)} although the model is at its usage limit: its agent would be stopped again at once.`
    : `Starts it on ${visibleText(t.model)} although the model is near its usage limit.`

/** Asks first, over the panel; resolves true when the user agreed, false when not or when the panel went away. Nothing is sent before. */
function confirmStart(t: TicketSummary, dialogs: DialogScope, store: object): Promise<boolean> {
  return new Promise<boolean>((done) => {
    let agreed = false
    const close = mountDialog(
      (cancel) =>
        h(
          UiDialog,
          { open: true, title: `Start #${t.number} anyway?`, eyebrow: `#${t.number}`, onClose: cancel, 'data-start-anyway-dialog': '' },
          {
            default: () => {
              const state = modelState(t, store)
              return [
                h('p', { class: 'm-0' }, sentence(t, state)),
                state === 'hard' ? null : h('p', { class: 'm-0 mt-2 text-sm' }, 'Its agent may be stopped again when the model reaches its hard limit.'),
              ]
            },
            actions: () => [
              h(UiButton, { onClick: cancel, 'data-start-anyway-cancel': '' }, () => 'Cancel'),
              h(
                UiButton,
                {
                  variant: 'primary',
                  'data-primary': '',
                  disabled: modelState(t, store) === 'hard',
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
  unavailable: (t, ctx) => startAnywayUnavailable(t, modelState(t, ctx.store)),
  describe: (t, ctx) => (t.model === '' ? 'Starts it although its model is near its usage limit.' : sentence(t, modelState(t, ctx.store))),
  run: async (t, ctx) => {
    if (!(await confirmStart(t, ctx.dialogs, ctx.store))) return 'cancelled'
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
