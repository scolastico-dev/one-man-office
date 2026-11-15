// What the sidebar adds to the rest of the app. Called once when the sidebar
// is first shown; calling it again changes nothing.
import { registerOpenTerminalAction } from './openTerminalAction'

let done = false

export function registerSidebarActions(): void {
  if (done) return
  done = true
  registerOpenTerminalAction()
}
