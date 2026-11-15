// What the sidebar adds to the rest of the app. Called once when the sidebar
// is first shown; calling it again changes nothing. Call it while a component
// of the app is being set up or mounted: it needs the app's router.
import { isNavigationFailure, NavigationFailureType, useRouter, type Router } from 'vue-router'
import { registerOpenTerminalAction } from './openTerminalAction'

/**
 * Leaves a ticket's panel for the board, with the filters the address
 * carries; the ticket's own keys go. Resolves false when the navigation was
 * refused (the panel keeps unsaved changes) and the panel is still open.
 */
export async function leavePanel(router: Router): Promise<boolean> {
  const { tab: _tab, comment: _comment, ...query } = router.currentRoute.value.query
  const failure = await router.push({ path: '/', query })
  return !isNavigationFailure(failure) || isNavigationFailure(failure, NavigationFailureType.duplicated)
}

let done = false

export function registerSidebarActions(): void {
  if (done) return
  const router = useRouter()
  done = true
  registerOpenTerminalAction({
    closePanel: () => leavePanel(router),
  })
}
