import type { ApiClient } from '../../api/client'
import { openProxyForward } from '../../api/endpoints'
import type { PluginRunResult } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'
import { closeUnusedTab, navigateReservedTab, reserveOpenTab } from './openForward'

function safeAddress(url: string): boolean {
  try {
    const parsed = new URL(url)
    return /^https?:\/\//i.test(url) && !parsed.username && !parsed.password && !url.includes('\\') && visibleText(url) === url && !/\s/.test(url)
  } catch { return false }
}

/** Call synchronously from a click. The callback may poll until an editor is ready. */
export async function navigatePluginResult(client: ApiClient, action: () => Promise<PluginRunResult>, active: () => boolean = () => true): Promise<PluginRunResult> {
  const tab = reserveOpenTab()
  try {
    const result = await action()
    if (!active()) throw new Error('The plugin is no longer active.')
    if (result.forward_id && result.url) throw new Error('The plugin returned two destinations.')
    let url = result.url
    if (url && !safeAddress(url)) throw new Error('The plugin returned an unsafe address.')
    if (result.forward_id) {
      if (!/^[a-f0-9]{32}$/.test(result.forward_id)) throw new Error('The plugin returned an invalid forward.')
      if (!tab || tab.closed) navigateReservedTab(tab, '')
      url = (await openProxyForward(client, result.forward_id)).data.url
    }
    if (!active()) throw new Error('The plugin is no longer active.')
    if (url) navigateReservedTab(tab, url)
    else closeUnusedTab(tab)
    return result
  } catch (e) {
    closeUnusedTab(tab)
    throw e
  }
}
