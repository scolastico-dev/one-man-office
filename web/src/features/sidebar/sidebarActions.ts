// What the sidebar adds to the rest of the app. Called once when the sidebar
// is first shown; calling it again changes nothing. Call it while a component
// of the app is being set up or mounted: it needs the app's router.
import { useRouter } from 'vue-router'
import { registerOpenTerminalAction } from './openTerminalAction'

let done = false

export function registerSidebarActions(): void {
  if (done) return
  const router = useRouter()
  done = true
  registerOpenTerminalAction({
    closePanel: () => {
      // Back to the board with the filters the address carries; the ticket's own keys go.
      const { tab: _tab, comment: _comment, ...query } = router.currentRoute.value.query
      return router.push({ path: '/', query })
    },
  })
}
