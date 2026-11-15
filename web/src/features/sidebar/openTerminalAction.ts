// The ticket action that opens a shell in the ticket's worktree and shows it
// in the sidebar's Terminals tab. The server resolves the folder; the browser
// only names the project and the ticket.
import { createApp, defineComponent, h, ref, type App } from 'vue'
import { failureText } from '../../api/lostAnswer'
import type { TicketProject, TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { registerTicketAction, type ActionContext, type TicketAction } from '../ticket/actions'
import { useSidebar } from './useSidebar'
import { terminalsOf } from './useTerminals'

export interface OpenTerminalDeps {
  /**
   * Leaves the ticket's panel (a modal dialog) so the sidebar can be used;
   * false when the panel stayed open (leaving it was refused).
   */
  closePanel(): boolean | void | Promise<boolean | void>
}

/** The ticket's projects that have a worktree to open a shell in. */
function worktrees(t: TicketSummary): TicketProject[] {
  return t.projects.filter((p) => p.worktree !== '')
}

/** Why no shell can be opened for `t`, in words; undefined when one can. */
export function openTerminalUnavailable(t: TicketSummary): string | undefined {
  if (t.column === 'done') return `Work on #${t.number} is finished; open a terminal in the project instead.`
  if (worktrees(t).length === 0) return `No worktree yet: work on #${t.number} has not started.`
  return undefined
}

/** What the project dialog ended with when the user closed it without choosing. */
export type ChoiceOutcome = 'cancelled'

/**
 * Asks which project's worktree to open; resolves once a shell is open, or
 * with 'cancelled' when the user closed the dialog. The server's refusal
 * stays in the dialog.
 */
function chooseProject(t: TicketSummary, ctx: ActionContext, open: (project: string) => Promise<void>): Promise<ChoiceOutcome | void> {
  const host = document.createElement('div')
  document.body.appendChild(host)
  return new Promise<ChoiceOutcome | void>((done) => {
    let app: App | null = null
    // Closing the dialog while it unmounts closes it again: only the first counts.
    const finish = (outcome?: ChoiceOutcome) => {
      const a = app
      if (a === null) return
      app = null
      a.unmount()
      host.remove()
      done(outcome)
    }
    const Choice = defineComponent({
      setup() {
        const busy = ref(false)
        const error = ref('')
        async function pick(project: string) {
          if (busy.value) return
          busy.value = true
          error.value = ''
          try {
            await open(project)
            finish()
          } catch (e) {
            error.value = failureText(e, { lost: 'omo did not answer; check the Terminals tab before trying again.', refused: 'The terminal could not be opened.' })
            busy.value = false
          }
        }
        return () =>
          h(
            UiDialog,
            { open: true, title: 'Open a terminal in which project?', eyebrow: `#${t.number}`, onClose: () => finish('cancelled') },
            {
              default: () => [
                h('p', { class: 'text-sm' }, `#${t.number} works in more than one project. The shell opens in the worktree of the one you choose.`),
                error.value ? h('p', { role: 'alert', class: 'text-sm', 'data-action-error': '' }, error.value) : null,
                h(
                  'div',
                  { class: 'flex flex-wrap gap-2 mt-3' },
                  worktrees(t).map((p) =>
                    h(UiButton, { disabled: busy.value, 'data-choose-project': p.project, onClick: () => void pick(p.project) }, () => visibleText(ctx.store.state.projects.get(p.project)?.name ?? p.project)),
                  ),
                ),
              ],
            },
          )
      },
    })
    app = createApp(Choice)
    app.mount(host)
  })
}

/** Builds the action; `deps` says how to leave the ticket's panel. */
export function createOpenTerminalAction(deps: OpenTerminalDeps): TicketAction {
  async function open(t: TicketSummary, ctx: ActionContext, project: string): Promise<void> {
    // The shell exists once the server answered; the panel closes after that,
    // so a refusal stays in the panel (or the project dialog) where it can be read.
    // Through the shared list, so the Terminals tab knows the shell before
    // the stream's terminal.opened arrives.
    const shell = await terminalsOf({ client: ctx.client, store: ctx.store }).open({ project, ticket: t.number })
    // The shell is open either way; the sidebar opens only once the panel is gone.
    if ((await deps.closePanel()) === false) return
    useSidebar().openOn('terminals', shell.id)
  }

  return {
    id: 'open-terminal',
    label: 'Open terminal',
    applies: () => true,
    unavailable: (t) => openTerminalUnavailable(t),
    describe: (t) => `Opens a shell in the worktree of #${t.number}; the worktree stays until the shell is closed.`,
    run: (t, ctx) => {
      const choices = worktrees(t)
      if (choices.length === 1) return open(t, ctx, choices[0]!.project)
      return chooseProject(t, ctx, (project) => open(t, ctx, project))
    },
  }
}

let unregister: (() => void) | null = null

/** Adds Open terminal to every ticket panel; calling it again changes nothing. */
export function registerOpenTerminalAction(deps: OpenTerminalDeps): void {
  if (unregister === null) unregister = registerTicketAction(createOpenTerminalAction(deps))
}
