import { reactive } from 'vue'
import type { ApiClient } from '../../api/client'
import { openProxyForward } from '../../api/endpoints'

/** Shared by ticket and overall lists so repeated activation cannot issue duplicate Opens. */
export const openingForwards = reactive(new Set<string>())

/** Reserve during the user gesture; awaiting the API first can lose permission to create a tab. */
export async function openForward(client: ApiClient, id: string): Promise<void> {
  if (openingForwards.has(id)) return
  openingForwards.add(id)
  let tab: Window | null = null
  try {
    tab = window.open('about:blank', '_blank')
    if (!tab) throw new Error('Allow new tabs for omo in your browser, then try Open again.')
    tab.opener = null
    const { data } = await openProxyForward(client, id)
    if (tab.closed) throw new Error('The new tab was closed. Try Open again.')
    tab.location.replace(data.url)
  } catch (e) {
    // Do not close a page the user navigated to while the API was pending.
    try {
      if (tab && !tab.closed && tab.location.href === 'about:blank') tab.close()
    } catch { /* A cross-origin page now belongs to the user. */ }
    throw e
  } finally {
    openingForwards.delete(id)
  }
}
