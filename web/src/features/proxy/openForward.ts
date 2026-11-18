import { reactive } from 'vue'
import type { ApiClient } from '../../api/client'
import { openProxyForward } from '../../api/endpoints'

/** Shared by ticket and overall lists so repeated activation cannot issue duplicate Opens. */
export const openingForwards = reactive(new Set<string>())

/** A detached reservation belongs to one click and can survive hook polling. */
export function reserveOpenTab(): Window | null {
  let tab: Window | null = null
  try {
    tab = window.open('about:blank', '_blank')
    if (tab) tab.opener = null
    return tab
  } catch {
    closeUnusedTab(tab)
    return null
  }
}

export function closeUnusedTab(tab: Window | null): void {
  try {
    if (tab && !tab.closed && tab.location.href === 'about:blank') tab.close()
  } catch { /* A cross-origin page now belongs to the user. */ }
}

export function navigateReservedTab(tab: Window | null, url: string): void {
  if (!tab) throw new Error('Allow new tabs for omo in your browser. The action already ran; do not repeat it just to open its answer.')
  if (tab.closed) throw new Error('The new tab was closed. Try Open again.')
  // Never replace a page the user chose while initialization was pending.
  if (tab.location.href !== 'about:blank') throw new Error('The reserved tab was navigated elsewhere.')
  tab.location.replace(url)
}

/** Reserve during the user gesture; awaiting the API first can lose permission to create a tab. */
export async function openForward(client: ApiClient, id: string): Promise<void> {
  if (openingForwards.has(id)) return
  openingForwards.add(id)
  let tab: Window | null = null
  try {
    tab = reserveOpenTab()
    if (!tab) throw new Error('Allow new tabs for omo in your browser, then try Open again.')
    const { data } = await openProxyForward(client, id)
    navigateReservedTab(tab, data.url)
  } catch (e) {
    closeUnusedTab(tab)
    throw e
  } finally {
    openingForwards.delete(id)
  }
}
