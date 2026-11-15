import { ApiError } from '../../api/client'
import type { CreateTicket, Review, Role, TaskRelease, TicketSummary, TicketType, WhenApproved } from '../../api/types'

/** The longest title the server accepts, in characters. */
export const TITLE_MAX = 200

/** The headings a new story's description starts with; the server uses the same text for an empty description. */
export const STORY_TEMPLATE = "## Goal\n\n\n## Do\n\n\n## Don't\n\n\n## Done when\n\n"

/** Fields of the create dialog that can show a message of their own. */
export type FieldKey =
  | 'title'
  | 'description'
  | 'projects'
  | 'story'
  | 'supervisor'
  | 'model'
  | 'review'
  | 'when_approved'
  | 'task_release'
  | 'max_parallel'
  | 'depends_on'

const FIELD_KEYS: readonly string[] = [
  'title',
  'description',
  'projects',
  'story',
  'supervisor',
  'model',
  'review',
  'when_approved',
  'task_release',
  'max_parallel',
  'depends_on',
]

/** Fields inside the "More options" disclosure. */
export const MORE_FIELDS: readonly FieldKey[] = ['supervisor', 'model', 'review', 'when_approved', 'task_release', 'max_parallel', 'depends_on']

/** Characters a title may not contain: controls, line and paragraph separators. */
const CONTROL = new RegExp('[\\u0000-\\u001f\\u007f\\u0080-\\u009f\\u2028\\u2029]', 'u')

/** What can be said about a title without asking the server; empty when it looks fine. */
export function titleProblem(title: string): string {
  const t = title.trim()
  if (t === '') return 'Enter a title.'
  if ([...t].length > TITLE_MAX) return `The title can have at most ${TITLE_MAX} characters.`
  if (CONTROL.test(t)) return 'The title must be one line without control characters.'
  return ''
}

/** Ticket numbers typed as "12, 15" or "#12 #15"; `null` when something else is in the text. */
export function parseNumbers(text: string): number[] | null {
  const parts = text.split(/[\s,]+/u).filter(Boolean)
  const out: number[] = []
  for (const p of parts) {
    const m = /^#?(\d{1,9})$/u.exec(p)
    if (!m || Number(m[1]) < 1) return null
    const n = Number(m[1])
    if (!out.includes(n)) out.push(n)
  }
  return out
}

export interface CreateForm {
  type: TicketType
  title: string
  description: string
  /** Chosen project ids (a task has at most one). */
  projects: string[]
  story: number | null
  supervisor: Role | ''
  model: string
  review: Review | ''
  whenApproved: WhenApproved | ''
  taskRelease: TaskRelease | ''
  maxParallel: string
  dependsOn: string
}

/** A fresh form: a story with the template; a task when a story is preset. */
export function emptyForm(initialStory?: number): CreateForm {
  const task = initialStory !== undefined
  return {
    type: task ? 'task' : 'story',
    title: '',
    description: task ? '' : STORY_TEMPLATE,
    projects: [],
    story: initialStory ?? null,
    supervisor: '',
    model: '',
    review: '',
    whenApproved: '',
    taskRelease: '',
    maxParallel: '',
    dependsOn: '',
  }
}

const isTemplate = (description: string) => description.trim() === STORY_TEMPLATE.trim()

/** Whether the user typed anything worth asking about before it is thrown away. */
export function isDirty(f: CreateForm): boolean {
  const description = f.description.trim()
  return f.title.trim() !== '' || (description !== '' && !isTemplate(description)) || f.dependsOn.trim() !== ''
}

/** Whether a task inside a story is being created (it is merged into the story: no "when approved"). */
export function inStory(f: CreateForm): boolean {
  return f.type === 'task' && f.story !== null
}

/**
 * The request for a form: fields of "More options" are sent only when they
 * differ from the default, so the server's defaults stay in charge. Returns
 * a message for the field instead when the form cannot be sent at all.
 */
export function buildRequest(f: CreateForm): { request: CreateTicket } | { field: FieldKey; message: string } {
  const problem = titleProblem(f.title)
  if (problem) return { field: 'title', message: problem }
  if (f.type === 'task' && f.projects.length !== 1) return { field: 'projects', message: 'Choose the project this task works in.' }
  const req: CreateTicket = { type: f.type, title: f.title.trim(), projects: f.type === 'task' ? f.projects.slice(0, 1) : f.projects }
  if (f.description.trim() !== '' && !isTemplate(f.description)) req.description = f.description
  if (f.type === 'task' && f.story !== null) req.story = f.story
  if (f.supervisor !== '') req.supervisor = f.supervisor
  if (f.model !== '') req.model = f.model
  if (f.review !== '') req.review = f.review
  if (f.whenApproved !== '' && !inStory(f)) req.when_approved = f.whenApproved
  if (f.type === 'story') {
    if (f.taskRelease !== '') req.task_release = f.taskRelease
    if (f.maxParallel.trim() !== '') {
      if (!/^\d{1,4}$/u.test(f.maxParallel.trim()) || Number(f.maxParallel) < 1) {
        return { field: 'max_parallel', message: 'Enter a whole number of at least 1, or leave it empty.' }
      }
      req.max_parallel = Number(f.maxParallel)
    }
  }
  const deps = parseNumbers(f.dependsOn)
  if (deps === null) return { field: 'depends_on', message: 'Enter ticket numbers separated by commas, like 12, 15.' }
  if (deps.length > 0) req.depends_on = deps
  return { request: req }
}

export interface FieldProblem {
  /** The field the server's answer concerns, or null for the top of the dialog. */
  field: FieldKey | null
  message: string
}

/** The server's message and, when the code or the message names it, the field it concerns. */
export function problemOf(e: unknown): FieldProblem {
  if (!(e instanceof ApiError)) return { field: null, message: 'The ticket could not be created.' }
  const d = e.details
  if (typeof d === 'object' && d !== null && 'field' in d) {
    const f = (d as { field: unknown }).field
    if (typeof f === 'string' && FIELD_KEYS.includes(f)) return { field: f as FieldKey, message: e.message }
  }
  return { field: fieldFor(e.code, e.message), message: e.message }
}

function fieldFor(code: string, message: string): FieldKey | null {
  if (code === 'project_unavailable') return 'projects'
  if (code === 'dependency_scope' || code === 'dependency_cycle') return 'depends_on'
  if (code === 'invalid_transition') return 'story'
  if (code !== 'validation') return null
  const m = message.toLowerCase()
  const rules: [RegExp, FieldKey][] = [
    [/^the title/u, 'title'],
    [/^task_release and max_parallel/u, 'max_parallel'],
    [/^(a task inside a story|when_approved)/u, 'when_approved'],
    [/^task_release/u, 'task_release'],
    [/^max_parallel/u, 'max_parallel'],
    [/^review must/u, 'review'],
    [/^model /u, 'model'],
    [/supervise/u, 'supervisor'],
    [/^dependency/u, 'depends_on'],
    [/^(a task has exactly one project|project )/u, 'projects'],
    [/^#\d+ is not a story/u, 'story'],
  ]
  for (const [re, key] of rules) if (re.test(m)) return key
  return null
}

const OPEN_STORY_COLUMNS = new Set(['planning', 'ready', 'in_progress'])

/** Stories that can still take a task: in Planning, Ready For Dev or In Progress. */
export function storiesForTasks(tickets: Iterable<TicketSummary>): TicketSummary[] {
  const out: TicketSummary[] = []
  for (const t of tickets) if (t.type === 'story' && OPEN_STORY_COLUMNS.has(t.column)) out.push(t)
  return out.sort((a, b) => a.number - b.number)
}
