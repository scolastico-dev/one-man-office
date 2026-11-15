// Shared setup of the Projects page tests: reads the fake omo does not know
// (settings with a revision, the scan state, folder listings) answered by
// the test.
import type { Query } from '../../../api/client'
import type { ScanState, SettingsResponse, WorkspaceRoot } from '../../../api/types'
import { general } from '../../../testing/fakeOmo'
import { ok, type Harness } from '../../create/dialogTesting'

export interface Reads {
  settings: SettingsResponse
  scan: ScanState
  /** Folder listings by `path` ('' for none); a missing one answers the home folder, empty. */
  folders: Record<string, { path: string; parent: string; folders: { name: string; path: string; repository: boolean; hidden: boolean; link: boolean }[] }>
  asked: string[]
}

export function settingsWith(roots: WorkspaceRoot[], revision = 'r1'): SettingsResponse {
  return { settings: { ...general, workspace_roots: roots }, errors: [], revision, home: '/home/you/.local/omo' }
}

export function answerReads(h: Harness, start: Partial<Reads> = {}): Reads {
  const reads: Reads = {
    settings: settingsWith([]),
    scan: { running: false, last: null },
    folders: {},
    asked: [],
    ...start,
  }
  const base = h.session.client.get
  h.session.client.get = (async (path: string, query?: Query) => {
    reads.asked.push(path)
    if (path === '/api/settings') return ok(structuredClone(reads.settings))
    if (path === '/api/projects/scan') return ok(structuredClone(reads.scan))
    if (path === '/api/folders') {
      const p = (query?.path as string | undefined) ?? ''
      const at = reads.folders[p] ?? { path: p || '/home/you', parent: '', folders: [] }
      return ok({ home: '/home/you', more: false, ...at })
    }
    return base(path, query)
  }) as typeof base
  return reads
}
