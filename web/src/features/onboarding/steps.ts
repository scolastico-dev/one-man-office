import { useCreateTicket } from '../create/useCreateTicket'
import { useSettingsDialogs } from '../topbar/settingsMenu'

/** What the steps look at to tick themselves off. */
export interface StepContext {
  /** How many projects omo knows. */
  projects: number
  /** How many tickets exist, open or done. */
  tickets: number
  /** Whether every role that needs a model has a usable one; null while that is not known yet. */
  agentsReady: boolean | null
}

export interface Step {
  id: string
  title: string
  text: string
  done(ctx: StepContext): boolean
  action: { label: string; run(): void }
}

function builtIn(): Step[] {
  return [
    {
      id: 'project',
      title: 'Add a project',
      text: 'A project is a folder with a git repository on this machine that omo may work in.',
      done: (c) => c.projects > 0,
      action: { label: 'Add project', run: () => useSettingsDialogs().open('addProject') },
    },
    {
      id: 'agents',
      title: 'Check the agent setup',
      text: 'Which agent programs and models each role will use, and what is missing.',
      done: (c) => c.agentsReady === true,
      action: { label: 'View agent setup', run: () => useSettingsDialogs().open('agents') },
    },
    {
      id: 'ticket',
      title: 'Create a first ticket',
      text: 'A story for a goal, or a task for one piece of work.',
      done: (c) => c.tickets > 0,
      action: { label: 'Create ticket', run: () => useCreateTicket().open() },
    },
  ]
}

const added: { step: Step; after: string | undefined }[] = []

/** Adds a step after the step with id `after`, or at the end. */
export function registerStep(step: Step, after?: string): void {
  added.push({ step, after })
}

/** The steps in order: the built-in three, then those registered. */
export function firstSteps(): Step[] {
  const list = builtIn()
  for (const { step, after } of added) {
    const i = after === undefined ? -1 : list.findIndex((s) => s.id === after)
    list.splice(i < 0 ? list.length : i + 1, 0, step)
  }
  return list
}
