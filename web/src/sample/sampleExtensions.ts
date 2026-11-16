// Static data for the samples of the usage, skill set and plugin pages
// (/__sample?settings=usage|skills|plugins) and of the usage panel in the
// Agents tab (&usage=1). The shapes follow the wire types the server sends
// (usage readings, scripts, test results, skill sets and their states,
// plugins, their hooks and log), written out here so the sample does not
// depend on the API module.
import { judgeWindow } from '../features/usage/usageVerdict'
import { windowTitle, windowWithName } from '../features/usage/windowLabel'
import { minutesAgo } from './sampleBoard'

/** A time this many minutes from now, as the server writes it. */
export function minutesAhead(min: number): string {
  return new Date(Date.now() + min * 60_000).toISOString()
}

// Usage

export interface SampleUsageWindow {
  name: string
  label: string
  used_percent: number
  resets_at: string | null
}

export interface SampleUsageReading {
  key: string
  script: string
  profiles: string[]
  windows: SampleUsageWindow[]
  fetched_at: string | null
  next_at: string | null
  running: boolean
  error: string
  stale_since: string | null
}

export type SampleUsageState = 'unmetered' | 'unknown' | 'ok' | 'soft' | 'hard'

export interface SampleModelUsage {
  model: string
  state: SampleUsageState
  window: string
  used_percent: number | null
  resets_at: string | null
  note: string
}

export interface SampleThreshold {
  soft_percent?: number
  hard_percent?: number
}

export const sampleUsageSettings = {
  soft_percent: 90,
  hard_percent: 98,
  refresh_interval: '5m',
  windows: { fable_weekly: { soft_percent: 80 } } as Record<string, SampleThreshold>,
}

/** A window's meter as the Agents tab judges it (see features/usage/usageVerdict). */
export function sampleMeter(w: SampleUsageWindow, r: Pick<SampleUsageReading, 'error' | 'fetched_at'>) {
  return { name: w.name, title: windowTitle(w), shown: windowWithName(w), ...judgeWindow(w, r, sampleUsageSettings, Date.now()) }
}

export const sampleReadings: SampleUsageReading[] = [
  {
    key: '3f9a1c7e2b604d18',
    script: 'claude',
    profiles: ['claude', 'claude-review'],
    windows: [
      { name: 'session', label: 'Session (5 hours)', used_percent: 42.5, resets_at: minutesAhead(200) },
      { name: 'weekly', label: 'Weekly, all models', used_percent: 91.2, resets_at: minutesAhead(4 * 24 * 60 + 90) },
      { name: 'fable_weekly', label: 'Weekly, Fable', used_percent: 99, resets_at: minutesAhead(2 * 24 * 60 + 30) },
    ],
    fetched_at: minutesAgo(2),
    next_at: minutesAhead(3),
    running: false,
    error: '',
    stale_since: null,
  },
  {
    key: 'b41e07d95c2a8f63',
    script: 'codex',
    profiles: ['codex'],
    windows: [],
    fetched_at: minutesAgo(7),
    next_at: minutesAhead(1),
    running: false,
    error: 'line 23: http request failed',
    stale_since: null,
  },
]

export const sampleModelUsage: SampleModelUsage[] = [
  { model: 'claude/fable', state: 'hard', window: 'fable_weekly', used_percent: 99, resets_at: minutesAhead(2 * 24 * 60 + 30), note: '' },
  { model: 'claude/opus', state: 'soft', window: 'weekly', used_percent: 91.2, resets_at: minutesAhead(4 * 24 * 60 + 90), note: '' },
  { model: 'claude/sonnet', state: 'soft', window: 'weekly', used_percent: 91.2, resets_at: minutesAhead(4 * 24 * 60 + 90), note: '' },
  { model: 'codex/default', state: 'unknown', window: '', used_percent: null, resets_at: null, note: '' },
  { model: 'gemini/default', state: 'unmetered', window: '', used_percent: null, resets_at: null, note: '' },
]

export interface SampleScriptInfo {
  name: string
  has_default: boolean
  customized: boolean
  used_by: string[]
}

export const sampleScripts: SampleScriptInfo[] = [
  { name: 'claude', has_default: true, customized: true, used_by: ['claude', 'claude-review'] },
  { name: 'codex', has_default: true, customized: false, used_by: ['codex'] },
  { name: 'local-proxy', has_default: false, customized: true, used_by: [] },
]

export const sampleScriptText = `-- Reads the Claude Code login and asks Anthropic how full each window is.
function usage(account)
  local dir = account.account_dir ~= "" and account.account_dir or "~/.claude"
  local raw, err = omo.read_file(dir .. "/.credentials.json")
  if not raw then error("no Claude login in " .. dir .. ": " .. err) end
  local token = omo.json_decode(raw).claudeAiOauth.accessToken

  local res = omo.http({
    url = "https://api.anthropic.com/api/oauth/usage",
    headers = { Authorization = "Bearer " .. token, ["anthropic-beta"] = "oauth-2025-04-20" },
  })
  if res.status ~= 200 then error("usage answered " .. res.status) end
  local u = omo.json_decode(res.body)

  local windows = {
    { name = "session", used = u.five_hour.utilization, resets_at = u.five_hour.resets_at },
    { name = "weekly", used = u.seven_day.utilization, resets_at = u.seven_day.resets_at },
  }
  if u.seven_day_fable then
    table.insert(windows, { name = "fable_weekly", used = u.seven_day_fable.utilization, resets_at = u.seven_day_fable.resets_at })
  end
  return windows
end
`

/** One line per helper a script can call. */
export const sampleHelpers: [call: string, what: string][] = [
  ['omo.http{url, method?, headers?, json?|form?|body?, timeout?}', 'https anywhere, http on this machine only; 8 calls, 1 MiB each'],
  ['omo.read_file(path)', 'a file below / or ~/, at most 1 MiB'],
  ['omo.env(name)', 'a variable of the agent environment of this profile'],
  ['omo.keychain(service)', 'a macOS keychain entry; nil elsewhere'],
  ['omo.rate_limited(retry_after)', 'ends the run as rate limited; omo waits Retry-After (1 min to 1 h), else 2, 4, 8 … min'],
  ['omo.codex_refresh(auth_path)', 'renews a Codex login with the profile\'s own command'],
  ['omo.json_decode(text), omo.json_encode(value)', 'JSON to tables and back'],
  ['omo.time_parse(rfc3339), omo.now()', 'Unix seconds'],
  ['omo.sha256_hex(text), omo.nfc(text)', 'hash and Unicode normal form'],
  ['omo.log(text)', 'a line for the test below; 50 lines per run'],
]

export interface SampleTestResult {
  profile: string
  windows: SampleUsageWindow[]
  error: string
  log: string[]
  millis: number
}

export const sampleTestOk: SampleTestResult = {
  profile: 'claude',
  windows: sampleReadings[0]!.windows,
  error: '',
  log: ['read ~/.claude/.credentials.json', 'GET https://api.anthropic.com/api/oauth/usage → 200', 'token [redacted], 3 windows'],
  millis: 812,
}

export const sampleTestFailed: SampleTestResult = {
  profile: 'claude-review',
  windows: [],
  error: 'line 5: no Claude login in /home/you/.claude-review: file does not exist',
  log: [],
  millis: 4,
}

export const sampleProfiles = ['claude', 'claude-review', 'codex', 'gemini']

// Skill sets

export type SampleSkillRole = 'ceo' | 'pm' | 'developer' | 'reviewer' | 'export'

export const SAMPLE_SKILL_ROLES: { id: SampleSkillRole; label: string }[] = [
  { id: 'ceo', label: 'CEO' },
  { id: 'pm', label: 'Product manager' },
  { id: 'developer', label: 'Developer' },
  { id: 'reviewer', label: 'Reviewer' },
  { id: 'export', label: 'Export' },
]

export interface SampleSkillSet {
  name: string
  repository: string
  ref: string
  update: 'at_start' | 'pinned'
  enabled: boolean
  instructions: Partial<Record<SampleSkillRole, string>>
}

export interface SampleSkillSetState {
  name: string
  state: 'missing' | 'updating' | 'ok' | 'failed'
  commit: string
  path: string
  updated_at: string | null
  message: string
}

export const sampleSkillSets: SampleSkillSet[] = [
  {
    name: 'superpowers',
    repository: 'https://github.com/obra/superpowers.git',
    ref: 'main',
    update: 'at_start',
    enabled: true,
    instructions: {
      pm: 'Before you split a story, read skills/brainstorming/SKILL.md and follow it.',
      developer: 'Before you write code, read skills/test-driven-development/SKILL.md and follow it.',
      reviewer: 'Review with skills/requesting-code-review/SKILL.md.',
    },
  },
  {
    name: 'team-conventions',
    repository: 'git@git.example.com:platform/agent-skills.git',
    ref: 'v2.3.0',
    update: 'pinned',
    enabled: true,
    instructions: { developer: 'Our commit and review rules are in CONVENTIONS.md.' },
  },
]

export const sampleSkillStates: SampleSkillSetState[] = [
  {
    name: 'superpowers',
    state: 'ok',
    commit: 'a41c9e07d2b85f6130c4e9a7b2d81f05c3e6a912',
    path: '/home/you/.local/omo/skills/superpowers',
    updated_at: minutesAgo(130),
    message: '',
  },
  {
    name: 'team-conventions',
    state: 'failed',
    commit: '',
    path: '/home/you/.local/omo/skills/team-conventions',
    updated_at: null,
    message: "git clone: fatal: Remote branch v2.3.0 not found in upstream origin",
  },
]

// Plugins

export type SamplePluginState = 'ready' | 'running' | 'error' | 'disabled' | 'not_installed' | 'invalid'

export interface SamplePluginHook {
  event: string
  name: string
  description: string
  scope: '' | 'ticket' | 'global'
  interval: string
}

export interface SamplePluginInfo {
  name: string
  version: string
  description: string
  state: SamplePluginState
  message: string
  commit: string
  last_event: string
  last_run_at: string | null
  hooks: SamplePluginHook[]
  source: string
  enabled: boolean
  config: Record<string, unknown>
}

const hook = (event: string, over: Partial<SamplePluginHook> = {}): SamplePluginHook => ({
  event,
  name: '',
  description: '',
  scope: '',
  interval: '',
  ...over,
})

export const samplePlugins: SamplePluginInfo[] = [
  {
    name: 'pushover',
    version: '2.0.0',
    description: 'Sends a push notification when a ticket needs you or an agent asks you a question.',
    state: 'ready',
    message: '',
    commit: 'bundled',
    last_event: 'question_asked',
    last_run_at: minutesAgo(12),
    hooks: [
      hook('ticket_needs_attention'),
      hook('question_asked'),
      hook('manual', { name: 'test', description: 'Send a test notification', scope: 'global' }),
      hook('manual', { name: 'send', description: 'Send this ticket to my phone', scope: 'ticket' }),
    ],
    source: 'builtin:pushover',
    enabled: true,
    config: { user_key: 'your-user-key', app_token: 'your-app-token', priority: 0, max_per_hour: 20 },
  },
  {
    name: 'ticket-log',
    version: '0.3.1',
    description: 'Writes every moved ticket and every comment to a file, one JSON line each.',
    state: 'error',
    message: 'hooks/moved.lua:14: attempt to index a nil value (field \'story\')',
    commit: '9c2e4f1b7a3d58e6021c4b9f7e3a6d1052b8c4e7',
    last_event: 'ticket_moved',
    last_run_at: minutesAgo(3),
    hooks: [
      hook('ticket_moved'),
      hook('comment_written'),
      hook('timer', { interval: '1h' }),
      hook('manual', { name: 'summary', description: 'Write a summary of today', scope: 'global' }),
      hook('manual', { name: 'mark', description: 'Mark this ticket in the log', scope: 'ticket' }),
    ],
    source: '/home/you/code/omo-ticket-log',
    enabled: true,
    config: { file: '~/notes/omo-tickets.jsonl', include_comments: true, rotate_after_lines: 5000 },
  },
]

export const sampleBundled = [{ name: 'pushover', description: 'Push notifications through pushover.net.' }]

export interface SamplePluginLogLine {
  at: string
  message: string
}

export const samplePluginLog: SamplePluginLogLine[] = [
  { at: minutesAgo(64), message: 'ticket_moved #12 planning → ready: wrote 1 line' },
  { at: minutesAgo(41), message: 'comment_written #13: wrote 1 line' },
  { at: minutesAgo(18), message: 'timer: rotated omo-tickets.jsonl (5000 lines)' },
  { at: minutesAgo(3), message: "ticket_moved #30: hooks/moved.lua:14: attempt to index a nil value (field 'story')" },
  { at: minutesAgo(3), message: 'ticket_moved #30: the hook failed; the next event runs it again' },
]

export const sampleActionResult = 'Wrote 14 tickets and 37 comments to ~/notes/omo-2026-11-15.md.'
