// Desktop (system) notifications: off until the user switches them on in
// this browser, which asks the browser for permission. Then only items that
// are new since the page loaded, and only while the page is hidden; the open
// page has its bell. Title and body are plain, single-line, visible text of
// bounded length, because they carry what agents wrote. A browser without the
// API, a refused permission or a constructor that throws means: nothing shows,
// and nothing breaks.
import { readonly, ref, type Ref } from 'vue'
import { visibleText } from '../../components/ui/visibleText'

/** The choice per browser: `on` or `off`. */
export const DESKTOP_KEY = 'omo.desktopNotifications'
export const TITLE_MAX = 80
export const BODY_MAX = 160

/** What a desktop notification shows; `key` names the item (also its tag, so several tabs alert once). */
export interface SystemItem {
  key: string
  title: string
  body: string
}

/** The part of the browser's `Notification` this module uses. */
export interface NotificationCtor {
  readonly permission: NotificationPermission
  requestPermission(): Promise<NotificationPermission>
  new (title: string, options?: NotificationOptions): { onclick: ((this: unknown, ev: Event) => unknown) | null; close(): void }
}

export interface SystemNotifierDeps<T extends SystemItem> {
  /** The browser's `Notification`; undefined where there is none. */
  Notification: NotificationCtor | undefined
  document: { readonly visibilityState: DocumentVisibilityState }
  storage: () => Storage | null
  /** A notification was clicked: open its item. */
  onClick(item: T): void
  /** Brings the tab to the front; `window.focus` by default. */
  focus?: () => void
  /** Keys of items present at start; they never notify. */
  initial?: Iterable<string>
}

export interface SystemNotifier<T extends SystemItem = SystemItem> {
  /** The browser has the API at all. */
  supported: boolean
  /** Switched on here and permitted by the browser. */
  enabled: Readonly<Ref<boolean>>
  /** Why switching on did not work, in words; empty otherwise. */
  reason: Readonly<Ref<string>>
  /** Asks the browser for permission (call only from a user action) and switches on when granted. */
  enable(): Promise<boolean>
  disable(): void
  /** Items that must never notify (present when the page loaded). */
  ignore(keys: Iterable<string>): void
  /** Shows the item when allowed; every key is considered once. Returns whether it showed. */
  notify(item: T): boolean
}

/** One line of visible text, at most `max` characters. */
export function plainText(s: string, max: number): string {
  const one = visibleText(s.replace(/\s+/g, ' ').trim())
  const chars = [...one]
  return chars.length <= max ? one : chars.slice(0, max - 1).join('') + '…'
}

function readChoice(storage: () => Storage | null): boolean {
  try {
    return storage()?.getItem(DESKTOP_KEY) === 'on'
  } catch {
    return false
  }
}

function writeChoice(storage: () => Storage | null, on: boolean): void {
  try {
    storage()?.setItem(DESKTOP_KEY, on ? 'on' : 'off')
  } catch {
    // Storage refused: the choice lasts until the page closes.
  }
}

export function createSystemNotifier<T extends SystemItem>(deps: SystemNotifierDeps<T>): SystemNotifier<T> {
  const api = deps.Notification
  const supported = typeof api === 'function'
  const permission = (): NotificationPermission | null => {
    try {
      return supported ? api!.permission : null
    } catch {
      return null
    }
  }
  const chosen = ref(supported && readChoice(deps.storage))
  const enabled = ref(chosen.value && permission() === 'granted')
  const reason = ref('')
  const known = new Set<string>(deps.initial ?? [])
  const focus = deps.focus ?? (() => window.focus())

  async function enable(): Promise<boolean> {
    reason.value = ''
    if (!supported) return false
    let answer: NotificationPermission
    try {
      answer = permission() === 'granted' ? 'granted' : await api!.requestPermission()
    } catch {
      answer = 'denied'
    }
    if (answer !== 'granted') {
      enabled.value = false
      reason.value =
        answer === 'denied'
          ? 'The browser blocked notifications for this page. Allow them in its site settings to switch this on.'
          : 'The browser did not allow notifications. Switch this on again to be asked once more.'
      return false
    }
    chosen.value = true
    enabled.value = true
    writeChoice(deps.storage, true)
    return true
  }

  function disable(): void {
    chosen.value = false
    enabled.value = false
    reason.value = ''
    writeChoice(deps.storage, false)
  }

  function notify(item: T): boolean {
    if (known.has(item.key)) return false
    known.add(item.key)
    if (!enabled.value || permission() !== 'granted' || deps.document.visibilityState !== 'hidden') return false
    try {
      const n = new api!(plainText(item.title, TITLE_MAX), { body: plainText(item.body, BODY_MAX), tag: item.key })
      n.onclick = () => {
        try {
          focus()
        } catch {
          // A browser that refuses to focus: the item still opens.
        }
        deps.onClick(item)
        n.close()
      }
      return true
    } catch {
      // Some browsers allow notifications only from a service worker.
      return false
    }
  }

  return {
    supported,
    enabled: readonly(enabled),
    reason: readonly(reason),
    enable,
    disable,
    ignore(keys) {
      for (const k of keys) known.add(k)
    },
    notify,
  }
}
