/** The user's theme choice: follow the system setting, or force one theme. */
export type ThemeChoice = 'system' | 'dark' | 'light'

/** localStorage key of the remembered choice. theme-boot.js reads the same key. */
export const THEME_KEY = 'omo.theme'

export const THEME_CHOICES: readonly { value: ThemeChoice; label: string }[] = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark' },
  { value: 'light', label: 'Light' },
]

/** The remembered choice; `system` when nothing usable is stored or storage is unavailable. */
export function readTheme(store: Storage): ThemeChoice {
  try {
    const value = store.getItem(THEME_KEY)
    return value === 'dark' || value === 'light' ? value : 'system'
  } catch {
    return 'system'
  }
}

/**
 * Shows the chosen theme by setting `data-theme` on `root` (removed for
 * `system`, so the stylesheet follows `prefers-color-scheme`), and remembers
 * the choice in `store` when one is given. Storage errors are ignored.
 */
export function applyTheme(choice: ThemeChoice, root: HTMLElement, store?: Storage): void {
  if (choice === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', choice)
  if (!store) return
  try {
    store.setItem(THEME_KEY, choice)
  } catch {
    // Private mode or blocked storage: the choice lasts until the page is closed.
  }
}
