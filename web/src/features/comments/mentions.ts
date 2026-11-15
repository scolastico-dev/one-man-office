// Which roles a comment on a ticket may mention, and whom the user may ask a
// question. This mirrors the board's rules (`internal/board/mentions.go`,
// `questionAddressee` in `internal/board/comments.go`); the server decides,
// and its refusal is shown when the two differ.
import type { AgentRef, Role, TicketSummary } from '../../api/types'
import { ROLE_LABELS } from '../board/model'

/**
 * The board's rule, in its words, so a reader can compare:
 * a task in a story accepts @developer, @reviewer, @pm, @supervisor, @ceo and @user;
 * a standalone task all but @pm; a story all but @developer; @ceo only while the CEO is on.
 */
export const MENTION_RULE =
  'a task in a story accepts @developer, @reviewer, @pm, @supervisor, @ceo and @user; ' +
  'a standalone task all but @pm; a story all but @developer; @ceo only while the CEO is on'

export interface MentionContext {
  ceoEnabled: boolean
  /** The agents of the ticket, to show their names beside the roles. */
  agents: readonly AgentRef[]
}

export interface MentionOption {
  /** What follows the `@`. */
  role: string
  /** The agent working as the role right now. */
  name?: string
}

const isStory = (t: TicketSummary) => t.type === 'story'
const inStory = (t: TicketSummary) => t.type === 'task' && t.story !== null

/** The role @supervisor stands for. */
export function effectiveSupervisor(t: TicketSummary, ctx: MentionContext): Role {
  return t.supervisor === 'ceo' && !ctx.ceoEnabled ? 'user' : t.supervisor
}

function nameOf(role: Role, ctx: MentionContext): string | undefined {
  const live = ctx.agents.filter((a) => a.role === role && a.state !== 'ended')
  return live[0]?.name
}

/** The names `@` completes to on the ticket, in the board's order, with the agent behind each. */
export function mentionOptions(t: TicketSummary, ctx: MentionContext): MentionOption[] {
  const out: MentionOption[] = []
  const add = (role: string, as: Role) => {
    const name = nameOf(as, ctx)
    out.push(name === undefined ? { role } : { role, name })
  }
  if (!isStory(t)) add('developer', 'developer')
  add('reviewer', 'reviewer')
  if (isStory(t) || inStory(t)) add('pm', 'pm')
  add('supervisor', effectiveSupervisor(t, ctx))
  if (ctx.ceoEnabled) add('ceo', 'ceo')
  add('user', 'user')
  return out
}

export interface AskTarget {
  /** The value of `CommentRequest.to`. */
  to: string
  label: string
}

/** Whom the user may ask: the product manager where one exists, the CEO while it is on (never oneself). */
export function askTargets(t: TicketSummary, ctx: MentionContext): AskTarget[] {
  const out: AskTarget[] = []
  if (isStory(t) || inStory(t)) out.push({ to: 'pm', label: capital(ROLE_LABELS.pm) })
  if (ctx.ceoEnabled) out.push({ to: 'ceo', label: ROLE_LABELS.ceo })
  return out
}

function capital(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/**
 * The mentions of a comment for the renderer: the roles the server parsed
 * (lower-case) to the name of the agent that was behind each when the comment
 * was written; `supervisor` too when the ticket's supervisor is among them.
 */
export function mentionNames(
  mentions: ReadonlyArray<{ role: Role; session?: string }>,
  t: TicketSummary | undefined,
  ctx: MentionContext,
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const m of mentions) {
    const name = m.session ? (ctx.agents.find((a) => a.session === m.session)?.name ?? '') : ''
    out[m.role] = name
    if (t && m.role === effectiveSupervisor(t, ctx)) out.supervisor = name
  }
  return out
}
