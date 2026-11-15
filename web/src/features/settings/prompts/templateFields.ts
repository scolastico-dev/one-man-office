// What a prompt template can use: the fields of omo's template data, with one
// line each. A test compares the names with the data omo renders prompts with
// and with the fields the default prompts use.
export interface TemplateField {
  name: string
  text: string
}

export const TEMPLATE_FIELDS: readonly TemplateField[] = [
  { name: 'Name', text: "The agent's name." },
  { name: 'Role', text: "The agent's role." },
  { name: 'Type', text: 'story or task.' },
  { name: 'Title', text: "The ticket's title." },
  { name: 'StoryTitle', text: 'The title of the story a task belongs to.' },
  { name: 'Supervisor', text: 'Who supervises the ticket, in words.' },
  { name: 'Verification', text: 'targeted or full.' },
  { name: 'TaskRelease', text: 'Who releases a finished task: agents or user.' },
  { name: 'WaitTimeout', text: 'How long omo ai wait waits.' },
  { name: 'Silent', text: 'The time without output after which an agent counts as silent.' },
  { name: 'Instructions', text: 'Your instructions for the role.' },
  { name: 'RestartNote', text: 'The restart prompt, on a fresh session of work that was already started.' },
  { name: 'Ticket', text: "The ticket's number." },
  { name: 'Story', text: "The number of the task's story; 0 for none." },
  { name: 'ReviewRound', text: 'The number of the review round.' },
  { name: 'Timeouts', text: 'How many waits in a row ran out.' },
  { name: 'Question', text: "The question's number." },
  { name: 'Projects', text: "The ticket's projects; walk them with range." },
  { name: 'MultiProject', text: 'True when the ticket uses more than one project.' },
  { name: 'CEOEnabled', text: 'True when the CEO is on.' },
  { name: 'CEOMayApprove', text: 'True when the CEO may approve stories.' },
  { name: 'AgentReview', text: 'True when an agent reviews the story.' },
]

/** The fields of one project inside `range .Projects`. */
export const PROJECT_FIELDS: readonly TemplateField[] = [
  { name: 'ID', text: "The project's id." },
  { name: 'Name', text: "The project's name." },
  { name: 'Folder', text: 'Your own checkout.' },
  { name: 'Worktree', text: "The agent's working folder." },
  { name: 'Branch', text: 'The branch checked out in the worktree.' },
  { name: 'Parent', text: 'The branch it was cut from; for a reviewer or an export, the branch it is compared with or targets.' },
]
