/** One entry of the gear menu in the top bar. */
export interface MenuEntry {
  id: string
  label: string
  open(): void
}

/**
 * The entries of the gear menu, in order: Projects, Theme and, once the
 * dialog exists, Agent setup. A later version replaces the list with the
 * settings pages. Empty for now.
 */
export function settingsMenu(): MenuEntry[] {
  return []
}
