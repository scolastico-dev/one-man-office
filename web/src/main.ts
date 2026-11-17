import { createApp } from 'vue'
import App from './App.vue'
import { createTokenStore } from './auth/token'
import { useToast } from './components/ui/useToast'
import { createAppRouter } from './router'
import { createSession, SESSION } from './session'
import { usePopouts } from './features/terminal/popout'
import { answerTokenAsks, askOpenerForToken, sameOriginOpener } from './features/terminal/windowToken'
import { applyTheme, readTheme } from './theme'
import './styles/main.css'

// First, before the router or anything else reads or records the address:
// move the login token out of it.
const tokens = createTokenStore()
tokens.capture(window.location, window.history)

// theme-boot.js already set the theme before the first paint; this keeps the two in step.
try {
  applyTheme(readTheme(window.localStorage), document.documentElement)
} catch {
  // Storage unavailable: the stylesheet follows the system setting.
}

const session = createSession({ tokens })
// The printed address opened in a tab that shows the app already only changes
// the fragment. The browser reports that with popstate first, then
// hashchange; the router reacts to popstate by recording the address in its
// history state, so the token must be gone before: these listeners are added
// before the router's. Each takes the token only once.
const addressChanged = () => session.addressChanged(window.location, window.history)
window.addEventListener('popstate', addressChanged)
window.addEventListener('hashchange', addressChanged)
const router = createAppRouter({
  onUnknownPath: (path) =>
    useToast().show({ title: 'Page not found', message: `There is nothing at ${path}. This is the board.` }),
})

// A terminal's own window of this origin may ask for the token when storage
// is blocked and this page holds it in memory only.
answerTokenAsks({ target: window, origin: window.location.origin, token: () => session.token() })

const app = createApp(App).provide(SESSION, session).use(router)
void router.isReady().then(async () => {
  // A terminal's own window without a stored token asks the page that opened it first.
  if (router.currentRoute.value.name === 'terminal') {
    await askOpenerForToken({ tokens, opener: sameOriginOpener(), target: window, origin: window.location.origin })
  } else {
    // Asks the open terminal windows now, long before the board shows a
    // terminal, so a terminal that is out never gets a second view for a moment.
    usePopouts()
  }
  app.mount('#app')
})
