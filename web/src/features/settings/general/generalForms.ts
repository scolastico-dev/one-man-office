// The form of some keys of the general settings (settings.yaml). Limits,
// Review and General each edit their own keys: a page reads the whole file,
// shows its keys, and saves only those with `PATCH /api/settings` and the
// revision it read. A change of other keys (a hand edit, another page) is no
// conflict for it: a refused save is sent again with the new revision while
// its own keys are still what it started from.
import { computed, getCurrentScope, onScopeDispose, ref, type ComputedRef, type Ref } from 'vue'
import type { ApiClient } from '../../../api/client'
import { getSettings, patchSettings } from '../../../api/endpoints'
import type { ApiEvent, General, SettingsChangedData, SettingsInvalidData, SettingsPatch } from '../../../api/types'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { createSettingsResource, type SaveOutcome, type SettingsResource, type SettingsResourceDeps } from '../useSettingsResource'

/** What a number field says while its text is no number (the field's own words). */
export const NUMBER_MESSAGE = 'Enter a whole number'
/** Shown with the Save bar while a field holds no number. */
export const BLOCKED_TEXT = 'Fix the fields marked above before saving.'

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
  /** A field tells that its text is no number (`message`), or is one again (`null`); Save is blocked meanwhile. */
  setParse(field: string, message: string | null): void
  /** The parse message of a field, or undefined. */
  parseOf(field: string): string | undefined
  dispose(): void
}

/** The form with explicit dependencies, for tests. `useGeneralForm` wires the app's. */
export function createGeneralForm<K extends keyof General>(keys: readonly K[], client: ApiClient, deps: SettingsResourceDeps): GeneralForm<K> {
  const home = ref('')
  const parse = ref<Record<string, string>>({})
  const tried = ref(false)
  const announce = deps.announce ?? { polite: () => {}, assertive: () => {} }

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

  const blocked = computed(() => Object.keys(parse.value).length > 0)
  const error = computed(() => (tried.value && blocked.value ? BLOCKED_TEXT : r.error.value))

  async function save(): Promise<SaveOutcome> {
    if (blocked.value) {
      tried.value = true
      announce.assertive(BLOCKED_TEXT)
      return 'refused'
    }
    return r.save()
  }

  return {
    ...r,
    error: error as ComputedRef<string>,
    save,
    home,
    setParse(field, message) {
      const next = { ...parse.value }
      if (message === null) delete next[field]
      else next[field] = message
      parse.value = next
      if (Object.keys(next).length === 0) tried.value = false
    },
    parseOf: (field) => parse.value[field],
  }
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
  return form
}

/** The refusals the server named for keys that are none of `placed` (the page's fields), in sentences. */
export function unplacedErrors(form: { fieldErrors: Readonly<Ref<Readonly<Record<string, string>>>> }, placed: readonly string[]): string[] {
  return Object.entries(form.fieldErrors.value)
    .filter(([field]) => !placed.includes(field))
    .map(([, message]) => message)
}

/**
 * A number field on one value of the form: `model` for the field's v-model,
 * `onParse` for its `parse` event, `error` for what to show under it (the
 * server's refusal, else the field's own words). An empty field is no number
 * either; the draft keeps its last number and Save stays blocked until the
 * field holds one.
 */
export function numberFieldOf<K extends keyof General>(form: GeneralForm<K>, field: string, get: () => number | null, set: (n: number) => void) {
  return {
    model: computed<number | null>({
      get,
      set(v) {
        if (v === null) {
          form.setParse(field, NUMBER_MESSAGE)
          return
        }
        form.setParse(field, null)
        set(v)
      },
    }),
    onParse: (message: string | null) => form.setParse(field, message),
    error: computed(() => form.fieldErrors.value[field] ?? form.parseOf(field)),
  }
}
