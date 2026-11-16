// The form of one agent profile and the way back to the wire.
//
// A profile is code that runs as the user: a command, its arguments and the
// variables it starts with, and the usage script that reads how full its
// account is. The form holds only what the page lets the user change;
// everything else of the loaded profile (fields this page does not know) is
// carried in `raw` and sent back exactly as it was read. The server decides
// what is valid; this file only converts.
import { ApiError } from '../../../api/client'
import type { DetachedRole, FileError, Profile, ProfileInUse, ProfileModel } from '../../../api/types'
import { visibleText } from '../../../components/ui/visibleText'
import { unplacedErrors as sharedUnplaced } from '../unplacedErrors'

/** The agent programs a profile can be for, in the order the form offers them. */
export const CLI_OPTIONS = [
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'custom', label: 'Custom' },
] as const

/** "Claude Code" for `claude`; an unknown type as it is. */
export function cliLabel(cli: string): string {
  return CLI_OPTIONS.find((o) => o.value === cli)?.label ?? cli
}

/** The variable the account folder becomes for agents of this program; empty for a custom program. */
export function accountVariable(cli: string): string {
  switch (cli) {
    case 'claude':
      return 'CLAUDE_CONFIG_DIR'
    case 'codex':
      return 'CODEX_HOME'
    case 'gemini':
      return 'GEMINI_CLI_HOME'
  }
  return ''
}

/** Whether the start prompt is delivered automatically: the program's default, on or off. */
export type InjectChoice = 'default' | 'on' | 'off'

export interface ModelForm {
  name: string
  /** Not stored yet: its name can still be changed. */
  isNew: boolean
  args: string[]
  env: Record<string, string>
  selectable: boolean
  description: string
  /** The windows of the profile's usage script that count for the model; empty: every window. */
  usage_windows: string[]
  /** The model as loaded (null for a new one); fields the form does not show come from here. */
  raw: ProfileModel | null
}

export interface ProfileForm {
  name: string
  cli: string
  command: string
  args: string[]
  env: Record<string, string>
  account_dir: string
  /** The usage script; empty: no usage readings (the models are unmetered). */
  usage_script: string
  inject: InjectChoice
  /** Go duration text; empty: the program's default. */
  delay: string
  retries: number | null
  /** Go duration text; empty: the program's default. */
  retry_wait: string
  models: ModelForm[]
  /** The profile as loaded (null for a profile not stored yet). */
  raw: Profile | null
}

/** The command a new profile of this program starts with. */
export function defaultCommand(cli: string): string {
  return cli === 'custom' ? '' : cli
}

/** A model the user just added. */
export function newModel(name = ''): ModelForm {
  return { name, isNew: true, args: [], env: {}, selectable: true, description: '', usage_windows: [], raw: null }
}

/** The form of a profile that is not stored yet: one model `default`, since a profile needs one. */
export function blankForm(name: string, cli: string): ProfileForm {
  return {
    name,
    cli,
    command: defaultCommand(cli),
    args: [],
    env: {},
    account_dir: '',
    usage_script: '',
    inject: 'default',
    delay: '',
    retries: null,
    retry_wait: '',
    models: [newModel('default')],
    raw: null,
  }
}

/** The form of a loaded profile; models by name. */
export function toForm(p: Profile): ProfileForm {
  const d = p.prompt_delivery ?? {}
  const models = p.models ?? {}
  return {
    name: p.name,
    cli: p.cli,
    command: p.command,
    args: [...(p.args ?? [])],
    env: { ...(p.env ?? {}) },
    account_dir: p.account_dir,
    usage_script: p.usage_script ?? '',
    inject: d.inject === undefined ? 'default' : d.inject ? 'on' : 'off',
    delay: d.delay ?? '',
    retries: d.retries ?? null,
    retry_wait: d.retry_wait ?? '',
    models: Object.keys(models)
      .sort((a, b) => a.localeCompare(b))
      .map((name) => {
        const m = models[name]!
        return {
          name,
          isNew: false,
          args: [...(m.args ?? [])],
          env: { ...(m.env ?? {}) },
          selectable: m.selectable ?? true,
          description: m.description,
          usage_windows: [...(m.usage_windows ?? [])],
          raw: JSON.parse(JSON.stringify(m)) as ProfileModel,
        }
      }),
    raw: JSON.parse(JSON.stringify(p)) as Profile,
  }
}

/**
 * The body of `PUT /api/profiles/{name}`: the loaded profile with the edited
 * fields put in; the revision is left out (it goes as If-Match).
 */
export function toProfile(f: ProfileForm): Profile {
  const raw: Partial<Profile> = f.raw === null ? {} : (JSON.parse(JSON.stringify(f.raw)) as Profile)
  delete raw.revision
  const delivery = { ...(raw.prompt_delivery ?? {}) }
  delete delivery.inject
  delete delivery.delay
  delete delivery.retries
  delete delivery.retry_wait
  if (f.inject !== 'default') delivery.inject = f.inject === 'on'
  if (f.delay.trim() !== '') delivery.delay = f.delay.trim()
  if (f.retries !== null) delivery.retries = f.retries
  if (f.retry_wait.trim() !== '') delivery.retry_wait = f.retry_wait.trim()

  const models: Record<string, ProfileModel> = {}
  for (const m of f.models) {
    const base: Partial<ProfileModel> = m.raw === null ? {} : (JSON.parse(JSON.stringify(m.raw)) as ProfileModel)
    const out: ProfileModel = {
      ...base,
      args: [...m.args],
      env: { ...m.env },
      // An empty list stays as the file had it (none or empty): both mean every window.
      usage_windows: m.usage_windows.length === 0 && base.usage_windows === null ? null : [...m.usage_windows],
      description: m.description,
    }
    // A missing value means selectable: do not write one the file did not have.
    if (m.selectable && base.selectable === undefined) delete out.selectable
    else out.selectable = m.selectable
    models[m.name] = out
  }

  return {
    ...raw,
    name: f.name,
    cli: f.cli,
    command: f.command,
    args: [...f.args],
    env: { ...f.env },
    account_dir: f.account_dir,
    usage_script: f.usage_script,
    prompt_delivery: delivery,
    models,
  }
}

/** A copy of a stored profile under a new name, for `Duplicate…`; the revision is left out. */
export function duplicateProfile(p: Profile, name: string): Profile {
  const copy = JSON.parse(JSON.stringify(p)) as Profile
  delete copy.revision
  copy.name = name
  return copy
}

/** The broken files of one profile, from the profile files the server reports. */
export function errorsOfProfile(errors: readonly FileError[], name: string): FileError[] {
  return errors.filter((e) => fileOfProfile(e.file) === name)
}

/** The profile a file belongs to (`…/profiles/<name>.yaml`), or null. */
export function fileOfProfile(file: string): string | null {
  const m = /(?:^|[\\/])profiles[\\/]([^\\/]+)\.ya?ml$/.exec(file)
  return m ? m[1]! : null
}

/** The field errors under `prefix.`, by the rest of the key (`env.` → by variable name). */
export function errorsUnder(fieldErrors: Readonly<Record<string, string>>, prefix: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(fieldErrors)) if (k.startsWith(`${prefix}.`)) out[k.slice(prefix.length + 1)] = v
  return out
}

/** The field errors of a list by row index (`args.2`). */
export function rowErrors(fieldErrors: Readonly<Record<string, string>>, prefix: string): Record<number, string> {
  const out: Record<number, string> = {}
  for (const [k, v] of Object.entries(errorsUnder(fieldErrors, prefix))) if (/^\d+$/.test(k)) out[Number(k)] = v
  return out
}

const PLACED_PROFILE = /^(cli|command|args|args\.\d+|env|env\.[^.]+|account_dir|usage_script|prompt_delivery\.(inject|delay|retries|retry_wait)|models)$/
const PLACED_MODEL = /^(args|args\.\d+|env|env\.[^.]+|usage_windows|usage_windows\.\d+|description)$/

/**
 * The field errors no field of the form shows (a field the page does not
 * edit, a model no longer in the form): shown at the top of the form, so no
 * refusal goes unseen.
 */
export function unplacedErrors(fieldErrors: Readonly<Record<string, string>>, models: readonly string[]): string[] {
  return sharedUnplaced(
    fieldErrors,
    // Model names may hold dots: match each name as a whole.
    (k) => PLACED_PROFILE.test(k) || models.some((m) => k.startsWith(`models.${m}.`) && PLACED_MODEL.test(k.slice(`models.${m}.`.length))),
  )
}

/** "a", "a and b", "a, b and c" */
function listWords(parts: readonly string[]): string {
  return parts.length < 2 ? (parts[0] ?? '') : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

const ROLE_WORDS: Record<string, string> = { ceo: 'CEO', pm: 'PM' }
/** A role's name as the page says it: CEO, PM, developer, a role of your own as named. */
export const roleWords = (role: string) => ROLE_WORDS[role] ?? visibleText(role)

/** The roles that keep a profile from being deleted (409 in_use with `details.roles`); null for any other refusal. */
export function rolesInUse(e: unknown): string[] | null {
  if (!(e instanceof ApiError) || e.status !== 409 || e.code !== 'in_use') return null
  const roles = (e.details as Partial<ProfileInUse> | undefined)?.roles
  return Array.isArray(roles) && roles.length > 0 && roles.every((r) => typeof r === 'string') ? roles : null
}

/** The second question before a profile that roles use is deleted. */
export function detachQuestion(profile: string, roles: readonly string[]): string {
  const which = roles.length === 1 ? 'the role' : 'the roles'
  const these = roles.length === 1 ? 'this role' : 'these roles'
  return `${visibleText(profile)} is used by ${which} ${listWords(roles.map(roleWords))}. Delete it and take its models out of ${these}?`
}

/** What a delete that took the profile's models out of roles did. */
export function detachedText(profile: string, detached: readonly DetachedRole[]): string {
  const done = `Profile ${visibleText(profile)} deleted.`
  if (detached.length === 0) return done
  const which = detached.length === 1 ? 'the role' : 'the roles'
  const roles = detached.map((d) => `${roleWords(d.role)} (${d.models.map(visibleText).join(', ')})`)
  return `${done} Its models were taken out of ${which} ${listWords(roles)}.`
}

/** What "Add missing models" did: the models added, or that none was missing. */
export function catalogAddedText(added: readonly string[], program: string): string {
  if (added.length === 0) return `No model was missing: the profile has every model omo suggests for ${program}.`
  return `Added the model${added.length === 1 ? '' : 's'} ${listWords(added.map(visibleText))}.`
}
