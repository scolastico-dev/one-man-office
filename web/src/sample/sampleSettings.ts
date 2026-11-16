// Static data for the settings sample (/__sample?settings=<section>). Shapes
// follow what the settings pages show, not the wire types: the sample only
// judges the look of the form components.

export type SampleSection =
  | 'projects'
  | 'agents'
  | 'roles'
  | 'usage'
  | 'prompts'
  | 'skills'
  | 'environment'
  | 'limits'
  | 'review'
  | 'plugins'
  | 'general'

/** The sections whose pages SampleExtensions draws. */
export type SampleExtensionSection = Extract<SampleSection, 'usage' | 'skills' | 'plugins'>
export const isExtensionSection = (s: SampleSection): s is SampleExtensionSection => s === 'usage' || s === 'skills' || s === 'plugins'

export interface SampleSectionDef {
  id: SampleSection
  label: string
  description: string
}

export const SAMPLE_SECTIONS: SampleSectionDef[] = [
  { id: 'projects', label: 'Projects', description: 'Workspace folders and the repositories found in them' },
  { id: 'agents', label: 'Agents', description: 'Agent programs, their accounts and models' },
  { id: 'roles', label: 'Roles', description: 'Which models each role works with' },
  { id: 'usage', label: 'Usage', description: 'Usage scripts and the limits that keep agents off busy models' },
  { id: 'prompts', label: 'Prompts', description: 'Your instructions and the prompts agents get' },
  { id: 'skills', label: 'Skill sets', description: 'Skill repositories agents are pointed at' },
  { id: 'environment', label: 'Environment', description: 'Variables every agent starts with' },
  { id: 'limits', label: 'Limits', description: 'Tasks and stories in work, health checks and time limits' },
  { id: 'review', label: 'Review', description: 'Review and release defaults for new tickets' },
  { id: 'plugins', label: 'Plugins', description: 'Plugins that react to the board' },
  { id: 'general', label: 'General', description: 'The CEO, merge defaults, this server and the theme' },
]

export interface SampleRoot {
  path: string
  maxDepth: number | null
  ignore: string[]
}

export const sampleRoots: SampleRoot[] = [
  { path: '~/workspace', maxDepth: 4, ignore: ['node_modules', '.*', 'archive/*'] },
  { path: '/srv/git', maxDepth: 2, ignore: [] },
]

export interface SampleProject {
  id: string
  name: string
  path: string
  root: string | null
  state: 'ready' | 'off' | 'missing'
}

export const sampleProjects: SampleProject[] = [
  { id: 'p1', name: 'github.com/not-three/api', path: '~/workspace/github.com/not-three/api', root: '~/workspace', state: 'ready' },
  { id: 'p2', name: 'github.com/not-three/ui', path: '~/workspace/github.com/not-three/ui', root: '~/workspace', state: 'ready' },
  { id: 'p3', name: 'homelab-infrastructure-ansible-playbooks', path: '/srv/git/homelab-infrastructure-ansible-playbooks', root: '/srv/git', state: 'off' },
  { id: 'p4', name: 'one-man-office', path: '~/code/one-man-office', root: null, state: 'ready' },
  { id: 'p5', name: 'github.com/old/prototype', path: '~/workspace/github.com/old/prototype', root: '~/workspace', state: 'missing' },
]

export const sampleProfileArgs = ['--dangerously-skip-permissions', '--verbose']

export const sampleProfileEnv: Record<string, string> = {
  ANTHROPIC_API_KEY: 'sk-ant-api03-example',
  CLAUDE_CODE_DISABLE_TELEMETRY: '1',
  HTTPS_PROXY: '$OMO_PROXY',
}

export interface SampleModel {
  name: string
  args: string[]
  env: Record<string, string>
  selectable: boolean
  description: string
}

export const sampleModels: SampleModel[] = [
  { name: 'opus', args: ['--model', 'claude-opus-4-6'], env: {}, selectable: true, description: 'Strongest; for planning and hard bugs.' },
  {
    name: 'sonnet',
    args: ['--model', 'claude-sonnet-4-6'],
    env: { MAX_THINKING_TOKENS: '8000' },
    selectable: true,
    description: 'Everyday development.',
  },
]

export const sampleRoleModels = [
  { id: 'claude/sonnet', label: 'claude/sonnet', note: 'Everyday development.' },
  { id: 'claude/sonnet', label: 'claude/sonnet', note: 'Everyday development.' },
  { id: 'claude/opus', label: 'claude/opus', note: 'Strongest; for planning and hard bugs.' },
  { id: 'codex/default', label: 'codex/default', note: 'does not exist: no profile codex' },
]

export const sampleShippedEnv: Record<string, string> = {
  GIT_TERMINAL_PROMPT: '0',
  GIT_EDITOR: 'true',
  PAGER: 'cat',
}

export const sampleAgentEnv: Record<string, string> = {
  ...sampleShippedEnv,
  PAGER: 'less -R',
  GITHUB_TOKEN: '`pass show github/omo`',
  OPENAI_API_KEY: 'sk-proj-example',
  PATH: '$HOME/.local/bin:$PATH',
}

export interface SampleEnvCheck {
  name: string
  state: 'set' | 'empty' | 'failed'
  reason?: string
}

export const sampleEnvCheck: SampleEnvCheck[] = [
  { name: 'GITHUB_TOKEN', state: 'set' },
  { name: 'OPENAI_API_KEY', state: 'set' },
  { name: 'PATH', state: 'set' },
  { name: 'GIT_EDITOR', state: 'set' },
  { name: 'NPM_TOKEN', state: 'failed', reason: 'its command did not finish within 10s' },
]

export const sampleInstructions = `Run the tests of the package you changed before you hand over.
Prefer small commits with conventional commit messages.
Never edit files under vendor/.`

export const samplePreview = `You are the developer on ticket #13, "Drag a card between columns",
in the project github.com/not-three/ui.

## Your instructions

Run the tests of the package you changed before you hand over.
Prefer small commits with conventional commit messages.
Never edit files under vendor/.`

/** Folder picker listing: names come from the disk and go through visibleText. */
export interface SampleFolder {
  name: string
  repository: boolean
  link: boolean
  hidden: boolean
}

export const samplePickerPath = '/home/you/workspace/github.com'

export const sampleFolders: SampleFolder[] = [
  { name: 'not-three', repository: false, link: false, hidden: false },
  { name: 'one-man-office', repository: true, link: false, hidden: false },
  { name: 'scolastico-dev', repository: false, link: false, hidden: false },
  { name: 'shared-notes', repository: false, link: true, hidden: false },
  { name: 'evil‮txt.exe', repository: false, link: false, hidden: false },
  { name: 'website', repository: true, link: false, hidden: false },
  { name: '.cache', repository: false, link: false, hidden: true },
]
