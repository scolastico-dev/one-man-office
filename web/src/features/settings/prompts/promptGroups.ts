// How the prompt list is laid out on the page: one group per role (its
// instructions first, then omo's prompt for the role) and one group of the
// short messages. Titles and explanations are the page's wording; the list
// from the server decides which prompts exist.
import type { PromptInfo, PromptKind } from '../../../api/types'

/** One prompt as a row of the page. */
export interface PromptEntry {
  name: string
  kind: PromptKind
  customized: boolean
  revision: string
  /** What the editor of this prompt is called. */
  title: string
  /** One line under the title. */
  description: string
  /** The text of the button that opens the editor. */
  action: string
}

export interface PromptGroup {
  id: string
  /** The group's name, shown in its border. */
  title: string
  description: string
  prompts: PromptEntry[]
}

/** The roles that have a prompt, in the order the page shows them. */
export const PROMPT_ROLES = ['ceo', 'pm', 'developer', 'reviewer', 'export'] as const

const ROLE_TITLES: Record<string, string> = { ceo: 'CEO', pm: 'Product manager', developer: 'Developer', reviewer: 'Reviewer', export: 'Export' }
const ROLE_NOUNS: Record<string, string> = { ceo: 'CEO', pm: 'product manager', developer: 'developer', reviewer: 'reviewer', export: 'export agent' }

const MESSAGES: Record<string, { title: string; description: string }> = {
  start: { title: 'Start', description: 'Typed into an agent when its session starts.' },
  restart: { title: 'Restart', description: "Added to the developer's prompt when a fresh session continues work that was already started." },
  handoff: { title: 'Handoff', description: 'Typed into an agent when omo stops, so it writes down where it stands.' },
  'wait-timeout': { title: 'Wait timeout', description: 'The answer of omo ai wait when nothing arrived in time.' },
  'wait-investigate': { title: 'Wait investigate', description: 'Added to that answer when several waits in a row ran out.' },
  stalled: { title: 'Stalled', description: 'Typed into an agent that has shown no output for the silent time.' },
  'question-reminder': { title: 'Question reminder', description: 'Typed into an agent that has not answered a question yet.' },
}

const SUFFIX = '-instructions'

/** The role an instructions prompt belongs to ("developer" for "developer-instructions"), or "". */
export function instructionsRole(name: string): string {
  return name.endsWith(SUFFIX) ? name.slice(0, -SUFFIX.length) : ''
}

function entryOf(p: PromptInfo): PromptEntry {
  const base = { name: p.name, kind: p.kind, customized: p.customized, revision: p.revision }
  if (p.kind === 'instructions') {
    const role = instructionsRole(p.name)
    const noun = ROLE_NOUNS[role] ?? role
    return {
      ...base,
      title: 'Your instructions',
      description: `Inserted into the ${noun}'s prompt. Empty, the prompt has no such part.`,
      action: 'Edit your instructions',
    }
  }
  if (p.kind === 'role') {
    return {
      ...base,
      title: "omo's prompt for this role",
      description: 'What omo tells the agent when it starts. A template: it uses the ticket, the projects and the settings.',
      action: "Show omo's prompt for this role",
    }
  }
  const known = MESSAGES[p.name]
  return {
    ...base,
    title: known?.title ?? p.name,
    description: known?.description ?? 'A text omo types into an agent.',
    action: 'Edit',
  }
}

/** A role group with every prompt of the list that belongs to the role, instructions first. */
function roleGroup(role: string, list: readonly PromptInfo[]): PromptGroup {
  const mine = list.filter((p) => (p.kind === 'role' && p.name === role) || (p.kind === 'instructions' && instructionsRole(p.name) === role))
  mine.sort((a, b) => Number(b.kind === 'instructions') - Number(a.kind === 'instructions'))
  return {
    id: role,
    title: ROLE_TITLES[role] ?? role,
    description: `What the ${ROLE_NOUNS[role] ?? role} is told.`,
    prompts: mine.map(entryOf),
  }
}

/**
 * Groups the prompts of `GET /api/prompts`: the five roles first (empty
 * groups are left out), then the messages. A name the page does not know
 * goes to its kind's group (an unknown role gets a group of its own), so no
 * prompt of the list is ever missing from the page.
 */
export function groupPrompts(list: readonly PromptInfo[]): PromptGroup[] {
  const roles: string[] = [...PROMPT_ROLES]
  for (const p of list) {
    const role = p.kind === 'instructions' ? instructionsRole(p.name) : p.kind === 'role' ? p.name : ''
    if (role !== '' && !roles.includes(role)) roles.push(role)
  }
  const groups = roles.map((r) => roleGroup(r, list)).filter((g) => g.prompts.length > 0)
  const messages = list.filter((p) => p.kind === 'message')
  if (messages.length > 0) {
    groups.push({
      id: 'messages',
      title: 'Messages',
      description: 'Short texts omo types into a running agent.',
      prompts: messages.map(entryOf),
    })
  }
  return groups
}
