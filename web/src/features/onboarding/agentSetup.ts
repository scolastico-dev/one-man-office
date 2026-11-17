// What the agent setup is, in words: for each role the models it will use and
// how it picks among them, where each model comes from, and what is missing.
// `describeSetup` is pure; `useAgentSetup` keeps one live copy of the data per
// session.
import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { ApiError } from '../../api/client'
import { getProfiles, getRoles, getSettings, listModels } from '../../api/endpoints'
import type { FileError, Model, Profile, RoleSettings } from '../../api/types'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { ROLE_LABELS } from '../board/model'

/** The roles that have a settings file, in the order they are shown. */
export const SETUP_ROLES = ['ceo', 'pm', 'developer', 'reviewer'] as const

export interface SetupData {
  roles: RoleSettings[]
  profiles: Profile[]
  models: Model[]
  profileErrors: FileError[]
  roleErrors: FileError[]
  ceoEnabled: boolean
}

export interface ModelLine {
  id: string
  /** `ok`: omo knows it and the role may use it; `unknown`: no profile defines it. A model that agents may not choose for tickets (`selectable: false`) is `ok` here. */
  state: 'ok' | 'unknown'
  /** The agent program, for example `claude`; empty when unknown. */
  program: string
  /** The profile that holds the account the model runs with; empty when unknown. */
  profile: string
  /** The account folder of that profile; empty means the program's own default account. */
  account: string
}

export interface RoleReport {
  name: string
  title: string
  /** Whether nothing may start without a usable model for it (every role but a switched-off CEO). */
  needed: boolean
  off: boolean
  /** How the role picks among its models, in words. */
  mode: string
  models: ModelLine[]
  usable: number
  /** What is wrong, one sentence each; empty when the role is fine. */
  problems: string[]
}

export interface SetupReport {
  roles: RoleReport[]
  /** The roles that need a usable model and have none. */
  blocking: RoleReport[]
  profileErrors: FileError[]
  /** Role files that cannot be read, not tied to one role. */
  otherRoleErrors: FileError[]
  profiles: { name: string; program: string; cli: string; models: string[] }[]
  hasProfiles: boolean
  /** A model id that works, to build the example from; empty when none does. */
  example: string
}

export const MODES: Record<string, string> = {
  round_robin: 'one after the other, in turn; a model listed twice is picked twice as often',
  random: 'at random; a model listed twice is picked twice as often',
  failover: 'the first one, and the next only when it cannot be used',
  smart: 'the one with the most capacity left',
}

export function roleTitle(name: string): string {
  const text = ROLE_LABELS[name as keyof typeof ROLE_LABELS] ?? name
  return name === 'ceo' ? text : text.charAt(0).toUpperCase() + text.slice(1)
}

function baseName(file: string): string {
  const leaf = file.split(/[\\/]/).pop() ?? file
  return leaf.replace(/\.[^.]*$/, '')
}

export function describeSetup(d: SetupData): SetupReport {
  const byId = new Map(d.models.map((m) => [m.id, m]))
  const profiles = new Map(d.profiles.map((p) => [p.name, p]))
  const reports: RoleReport[] = SETUP_ROLES.map((name) => {
    const role = d.roles.find((r) => r.name === name)
    const needed = name !== 'ceo' || d.ceoEnabled
    const off = name === 'ceo' && !d.ceoEnabled
    const ids = [...new Set(role?.models ?? [])]
    const models = ids.map<ModelLine>((id) => {
      const m = byId.get(id)
      if (m === undefined) return { id, state: 'unknown', program: '', profile: '', account: '' }
      const p = profiles.get(m.profile)
      return { id, state: 'ok', program: p?.command || m.cli, profile: m.profile, account: p?.account_dir ?? '' }
    })
    const usable = models.filter((m) => m.state === 'ok').length
    const title = roleTitle(name)
    const lower = name === 'ceo' ? 'CEO' : title.toLowerCase()
    const problems: string[] = []
    const broken = d.roleErrors.find((e) => baseName(e.file) === name)
    if (broken) problems.push(`The file ${broken.file} cannot be used: ${broken.message}`)
    if (ids.length === 0) {
      if (needed) problems.push(`No model assigned. Nothing starts for the ${lower} until it has one.`)
    } else {
      for (const m of models) {
        if (m.state === 'unknown') problems.push(`${m.id} does not exist (any more): no profile defines it.`)
      }
      if (usable === 0 && needed) problems.push(`No usable model left. Nothing starts for the ${lower} until it has one.`)
    }
    return { name, title, needed, off, mode: MODES[role?.assignment ?? ''] ?? role?.assignment ?? '', models, usable, problems }
  })
  const known = new Set(SETUP_ROLES as readonly string[])
  return {
    roles: reports,
    blocking: reports.filter((r) => r.needed && r.usable === 0),
    profileErrors: d.profileErrors,
    otherRoleErrors: d.roleErrors.filter((e) => !known.has(baseName(e.file))),
    profiles: d.profiles.map((p) => ({ name: p.name, program: p.command || p.cli, cli: p.cli, models: Object.keys(p.models ?? {}).sort() })),
    hasProfiles: d.profiles.length > 0,
    example: d.models.find((m) => m.selectable)?.id ?? '',
  }
}

export interface AgentSetup {
  loading: Ref<boolean>
  loaded: Ref<boolean>
  failure: Ref<string>
  report: ComputedRef<SetupReport | null>
  /** True when every role that needs a model has a usable one; null while unknown. */
  ready: ComputedRef<boolean | null>
  reload(): Promise<void>
}

const cache = new WeakMap<object, AgentSetup>()

/**
 * The agent setup of this session, read once and read again whenever a
 * profile or role file changes (also when it is edited by hand) and after
 * every board resync.
 */
export function useAgentSetup(): AgentSetup {
  const session = useSession()
  const store = useBoard()
  const hit = cache.get(store)
  if (hit) return hit

  const loading = ref(false)
  const loaded = ref(false)
  const failure = ref('')
  const data = ref<Omit<SetupData, 'ceoEnabled'> | null>(null)
  let attempt = 0

  async function reload() {
    const my = ++attempt
    loading.value = true
    try {
      const [r, p, m, s] = await Promise.all([
        getRoles(session.client),
        getProfiles(session.client),
        listModels(session.client),
        getSettings(session.client),
      ])
      if (my !== attempt) return
      data.value = {
        roles: r.data,
        profiles: p.data.profiles,
        models: m.data,
        profileErrors: p.data.errors ?? [],
        roleErrors: (s.data.errors ?? []).filter((e) => /[\\/]roles[\\/][^\\/]*$/.test(e.file)),
      }
      failure.value = ''
      loaded.value = true
    } catch (e) {
      if (my === attempt) failure.value = e instanceof ApiError ? e.message : 'The agent setup could not be read.'
    } finally {
      if (my === attempt) loading.value = false
    }
  }

  const report = computed(() => {
    if (data.value === null) return null
    return describeSetup({ ...data.value, ceoEnabled: store.state.general?.ceo.enabled !== false })
  })
  const ready = computed(() => (report.value === null ? null : report.value.blocking.length === 0))

  store.onEvent('settings.changed', (ev) => {
    const section = (ev.data as { section?: string } | null)?.section
    if (section === 'profiles' || section === 'roles') void reload()
  })
  store.onEvent('settings.invalid', () => void reload())
  store.onSnapshot(() => {
    if (loaded.value) void reload()
  })
  void reload()

  const setup: AgentSetup = { loading, loaded, failure, report, ready, reload }
  cache.set(store, setup)
  return setup
}
