// The pieces of the Projects page that are not drawing: the workspace
// folders as one settings item (only `workspace_roots` of settings.yaml), the
// project list in the order it is shown, and the words for scans.
import type { ApiClient } from '../../../api/client'
import { getSettings, patchSettings } from '../../../api/endpoints'
import { compareTime } from '../../../api/time'
import type { ApiEvent, FileError, Project, RootScan, ScanReport, SettingsChangedData, SettingsInvalidData, WorkspaceRoot } from '../../../api/types'
import type { SettingsResourceOptions } from '../useSettingsResource'

/** What the Workspace folders form edits. */
export interface RootsForm {
  workspace_roots: WorkspaceRoot[]
}

/** Ignore patterns a new workspace folder starts with: dependency folders and hidden folders. */
export const DEFAULT_IGNORE: readonly string[] = ['node_modules', '.*']
/** Search depth of a new workspace folder. */
export const DEFAULT_MAX_DEPTH = 4

export function newRoot(path: string): WorkspaceRoot {
  return { path, max_depth: DEFAULT_MAX_DEPTH, ignore: [...DEFAULT_IGNORE] }
}

/** The settings file itself, as opposed to a profile, role or project file. */
export function isSettingsFile(file: string): boolean {
  return file.split(/[\\/]/).pop() === 'settings.yaml'
}

/** Whether an event may have changed the workspace folders. */
export function watchesRoots(ev: ApiEvent): boolean {
  if (ev.kind === 'settings.changed') return (ev.data as Partial<SettingsChangedData> | null)?.section === 'general'
  if (ev.kind === 'settings.invalid') {
    const file = (ev.data as Partial<SettingsInvalidData> | null)?.file
    return typeof file === 'string' && isSettingsFile(file)
  }
  return false
}

const rootsOf = (roots: WorkspaceRoot[] | null | undefined): RootsForm => ({
  workspace_roots: (roots ?? []).map((r) => ({ path: r.path, max_depth: r.max_depth, ignore: [...(r.ignore ?? [])] })),
})

const settingsErrors = (errors: FileError[] | null | undefined) => (errors ?? []).filter((e) => isSettingsFile(e.file))

/** The workspace folders as a settings item: read from the settings, saved with PATCH of `workspace_roots` alone. */
export function rootsResourceOptions(client: ApiClient): SettingsResourceOptions<RootsForm> {
  return {
    async load() {
      const { data } = await getSettings(client)
      return { value: rootsOf(data.settings.workspace_roots), revision: data.revision, errors: settingsErrors(data.errors) }
    },
    async save(draft, revision) {
      const { data, eventId } = await patchSettings(client, { workspace_roots: draft.workspace_roots }, revision)
      return { value: rootsOf(data.settings.workspace_roots), revision: data.revision, eventId }
    },
    watches: watchesRoots,
    keys: ['workspace_roots'],
  }
}

/** Projects by name, then id. */
export function sortProjects(list: Iterable<Project>): Project[] {
  return [...list].sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
}

/** Where a project came from, in words (the root still needs `visibleText`). */
export function originText(p: Pick<Project, 'root'>): string {
  return p.root ? `found in ${p.root}` : 'added by hand'
}

/**
 * The path of `path` below the workspace folder `root`, slash separated, or
 * null when it does not lie below it. `home` expands a root written as `~`.
 */
export function pathBelow(path: string, root: string, home: string | null): string | null {
  let base = root
  if (base === '~' || base.startsWith('~/')) {
    if (home === null) return null
    base = home.replace(/\/+$/, '') + base.slice(1)
  }
  base = base.replace(/\/+$/, '')
  if (base === '' || !path.startsWith(base + '/')) return null
  const rel = path.slice(base.length + 1).replace(/\/+$/, '')
  return rel === '' ? null : rel
}

/** "2 added", "nothing new", with missing projects and folders that had a problem. */
export function scanSummary(r: ScanReport): string {
  const parts = [r.added.length > 0 ? `${r.added.length} added` : 'nothing new']
  if (r.missing > 0) parts.push(`${r.missing} missing`)
  const trouble = r.roots.filter((x) => x.state !== 'ok').length
  if (trouble > 0) parts.push(trouble === 1 ? '1 folder with a problem' : `${trouble} folders with a problem`)
  return parts.join(', ')
}

const STATE_WORDS: Record<string, string> = {
  ok: 'searched',
  missing: 'the folder does not exist',
  not_folder: 'not a folder',
  failed: 'could not be searched',
  cut: 'stopped at its limit; folders past it were not searched',
}

/** One workspace folder's result in words. */
export function rootStateText(r: Pick<RootScan, 'state'>): string {
  return STATE_WORDS[r.state] ?? r.state
}

/** "120 folders looked at, 3 repositories, 1 added". */
export function rootCounts(r: RootScan): string {
  const n = (k: number, one: string, many: string) => `${k} ${k === 1 ? one : many}`
  const parts = [n(r.visited, 'folder looked at', 'folders looked at'), n(r.found, 'repository', 'repositories'), `${r.added.length} added`]
  if (r.links > 0) parts.push(n(r.links, 'link not followed', 'links not followed'))
  return parts.join(', ')
}

/** Whether `a` finished later than `b` (RFC 3339 texts from the server). */
export function newerReport(a: ScanReport, b: ScanReport | null): boolean {
  if (b === null) return true
  return compareTime(a.finished_at, b.finished_at) >= 0
}
