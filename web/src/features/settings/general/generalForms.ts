// The form of some keys of the general settings (settings.yaml). Limits,
// Review and General each edit their own keys: a page reads the whole file,
// shows its keys, and saves only those with `PATCH /api/settings` and the
// revision it read. A change of other keys (a hand edit, another page) is no
// conflict for it: a refused save is sent again with the new revision while
// its own keys are still what it started from.
import { getCurrentScope, onScopeDispose, ref, type Ref } from 'vue'
import type { ApiClient } from '../../../api/client'
import { getSettings, patchSettings } from '../../../api/endpoints'
import type { ApiEvent, General, SettingsChangedData, SettingsInvalidData, SettingsPatch } from '../../../api/types'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { createSettingsResource, provideFormFields, type SettingsResource, type SettingsResourceDeps } from '../useSettingsResource'

/** The general settings with only `keys`, as a copy. */
export function pickGeneral<K extends keyof General>(g: General, keys: readonly K[]): Pick<General, K> {
  const out = {} as Pick<General, K>
  for (const k of keys) out[k] = JSON.parse(JSON.stringify(g[k])) as General[K]
  return out
}

const isSettingsFile = (file: string) => /(^|[\\/])settings\.yaml$/.test(file)

/** Whether an event may concern settings.yaml: a change of the general section, or its file found broken. */
export function watchesGeneral(ev: ApiEvent): boolean {
  if (ev.kind === 'settings.changed') return (ev.data as Partial<SettingsChangedData> | null)?.section === 'general'
  if (ev.kind === 'settings.invalid') return isSettingsFile((ev.data as Partial<SettingsInvalidData> | null)?.file ?? '')
  return false
}

export interface GeneralForm<K extends keyof General> extends SettingsResource<Pick<General, K>> {
  /** The omo home folder, as of the last read. */
  home: Readonly<Ref<string>>
  dispose(): void
}

/** The form with explicit dependencies, for tests. `useGeneralForm` wires the app's. */
export function createGeneralForm<K extends keyof General>(keys: readonly K[], client: ApiClient, deps: SettingsResourceDeps): GeneralForm<K> {
  const home = ref('')

  const r = createSettingsResource<Pick<General, K>>(
    {
      async load() {
        const { data } = await getSettings(client)
        home.value = data.home
        return {
          value: pickGeneral(data.settings, keys),
          revision: data.revision,
          errors: data.errors,
        }
      },
      async save(draft, revision) {
        const { data, eventId } = await patchSettings(client, draft as SettingsPatch, revision)
        home.value = data.home
        return {
          value: pickGeneral(data.settings, keys),
          revision: data.revision,
          eventId,
        }
      },
      watches: watchesGeneral,
      keys: [...keys] as string[],
    },
    deps,
  )

  return { ...r, home }
}

/**
 * The form of `keys` of the general settings for the calling page, on the
 * app's session and event stream; it stops with the page.
 */
export function useGeneralForm<K extends keyof General>(keys: readonly K[]): GeneralForm<K> {
  const { client } = useSession()
  const form = createGeneralForm(keys, client, {
    store: useBoard(),
    announce: useAnnouncer(),
  })
  if (getCurrentScope()) onScopeDispose(form.dispose)
  provideFormFields(form)
  return form
}
