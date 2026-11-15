import { createApp } from 'vue'
import App from './App.vue'
import { createTokenStore } from './auth/token'
import { useToast } from './components/ui/useToast'
import { createAppRouter } from './router'
import { createSession, SESSION } from './session'
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
const router = createAppRouter({
  onUnknownPath: (path) =>
    useToast().show({ title: 'Page not found', message: `There is nothing at ${path}. This is the board.` }),
})

const app = createApp(App).provide(SESSION, session).use(router)
void router.isReady().then(() => app.mount('#app'))
