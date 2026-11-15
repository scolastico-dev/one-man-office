// The Start anyway… action: starts a ticket that waits for usage on its own
// model although the model is near its limit. The server decides whether it
// can be done; its refusal is shown in the panel by the actions row.
import { createApp, defineComponent, h, type App } from 'vue'
import { startAnyway } from '../../api/endpoints'
import { isLostAnswer } from '../../api/lostAnswer'
import type { TicketSummary } from '../../api/types'
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

/** Asks first; resolves true when the user agreed. Nothing is sent before. */
function confirmStart(t: TicketSummary): Promise<boolean> {
  const host = document.createElement('div')
  document.body.appendChild(host)
  return new Promise<boolean>((done) => {
    let app: App | null = null
    // Closing the dialog while it unmounts closes it again: only the first counts.
    const finish = (agreed: boolean) => {
      const a = app
      if (a === null) return
      app = null
      a.unmount()
      host.remove()
      done(agreed)
    }
    const Ask = defineComponent({
      setup() {
        return () =>
          h(
            UiDialog,
            { open: true, title: `Start #${t.number} anyway?`, eyebrow: `#${t.number}`, onClose: () => finish(false), 'data-start-anyway-dialog': '' },
            {
              default: () => [
                h('p', { class: 'm-0' }, sentence(t)),
                h('p', { class: 'm-0 mt-2 text-sm' }, 'Its agent may be stopped again when the model reaches its hard limit.'),
              ],
              actions: () => [
                h(UiButton, { onClick: () => finish(false), 'data-start-anyway-cancel': '' }, () => 'Cancel'),
                h(UiButton, { variant: 'primary', 'data-primary': '', onClick: () => finish(true), 'data-start-anyway-confirm': '' }, () => 'Start anyway'),
              ],
            },
          )
      },
    })
    app = createApp(Ask)
    app.mount(host)
  })
}

export const startAnywayAction: TicketAction = {
  id: 'start-anyway',
  label: 'Start anyway…',
  applies: waitsForUsage,
  unavailable: (t) => startAnywayUnavailable(t),
  describe: (t) => (t.model === '' ? 'Starts it although its model is near its usage limit.' : sentence(t)),
  run: async (t, ctx) => {
    if (!(await confirmStart(t))) return 'cancelled'
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
