// One role's settings file as a settings item: loaded with its revision (and
// the broken role files of the settings answer), saved with If-Match through
// PUT /api/roles/{role}, read again when that role or its file changes.
import type { ApiClient } from '../../../api/client'
import { getRoles, getSettings, putRole } from '../../../api/endpoints'
import { NO_REVISION, type ApiEvent, type FileError, type RoleSettings, type SettingsChangedData, type SettingsInvalidData } from '../../../api/types'
import type { SettingsResourceOptions } from '../useSettingsResource'

/** What a role's form edits. */
export type RoleDraft = Pick<RoleSettings, 'name' | 'models' | 'assignment'>

/** Whether a settings file is this role's. */
export const isRoleFile = (role: string, file: string) => new RegExp(`[\\\\/]roles[\\\\/]${role}\\.ya?ml$`).test(file)

const plain = (r: RoleSettings): RoleDraft => ({ name: r.name, models: [...r.models], assignment: r.assignment })

export function roleResourceOptions(client: ApiClient, role: string): SettingsResourceOptions<RoleDraft> {
  return {
    load: async () => {
      const [roles, settings] = await Promise.all([getRoles(client), getSettings(client)])
      const found = roles.data.find((x) => x.name === role)
      const value = found === undefined ? { name: role, models: [], assignment: 'round_robin' } : plain(found)
      const errors: FileError[] = (settings.data.errors ?? []).filter((e) => isRoleFile(role, e.file))
      return { value, revision: found?.revision ?? NO_REVISION, errors }
    },
    save: async (draft, revision) => {
      const { data, eventId } = await putRole(client, { ...draft, models: [...draft.models] }, revision)
      return { value: plain(data), revision: data.revision ?? NO_REVISION, eventId }
    },
    watches: (ev: ApiEvent) => {
      if (ev.kind === 'settings.changed') {
        const d = ev.data as Partial<SettingsChangedData> | null
        return d?.section === 'roles' && d.name === role
      }
      if (ev.kind === 'settings.invalid') return isRoleFile(role, String((ev.data as Partial<SettingsInvalidData> | null)?.file ?? ''))
      return false
    },
  }
}
