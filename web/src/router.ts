import { createRouter, createWebHistory, type RouteRecordRaw, type Router, type RouterHistory } from 'vue-router'
import { lazyRoute } from './components/ui/chunkFailure'
import SettingsRouteFailed from './layout/SettingsRouteFailed.vue'
import { TICKET_PAGE_ROUTE } from './locations'

/** What the board shows lives in the app itself; routes only name the place. The settings are their own (lazily loaded) view. */
const Nothing = { render: () => null }

export interface AppRouterOptions {
  history?: RouterHistory
  /** Called with the path of an address that names nothing; the router then shows the board. */
  onUnknownPath?(path: string): void
}

/**
 * The app's addresses: `/` the board, `/<n>` the board with ticket #n open,
 * `/ticket/<n>` ticket #n on a page of its own in place of the board's columns,
 * `/settings/<section>` the settings in place of the board (`/settings` alone:
 * the section list), `/terminal/agent/<id>` and `/terminal/user/<id>` one
 * terminal alone (its own window), `/oauth/authorize` the consent page of a
 * program that asks for access to omo's MCP endpoint, and, in development only, `/__sample` the sample board.
 * Any other address shows the board, keeping its query. No address ever
 * carries the login token. Addresses are built in `locations.ts`.
 */
export function createAppRouter(o: AppRouterOptions = {}): Router {
  const routes: RouteRecordRaw[] = [
    { path: '/', name: 'board', component: Nothing },
    { path: '/:n(\\d+)', name: 'ticket', component: Nothing },
    { path: '/ticket/:n(\\d+)', name: TICKET_PAGE_ROUTE, component: lazyRoute(() => import('./features/ticket/page/TicketPage.vue')) },
    { path: '/settings/:section?', name: 'settings', component: lazyRoute(() => import('./features/settings/SettingsView.vue'), SettingsRouteFailed) },
    // One terminal in a window of its own; the app shows it in place of the board.
    { path: '/terminal/:kind(agent|user)/:id', name: 'terminal', component: Nothing },
    // The OAuth consent page of omo's MCP endpoint; the app shows it in place of the board.
    { path: '/oauth/authorize', name: 'oauth-consent', component: Nothing },
  ]
  if (import.meta.env.DEV) {
    routes.push({ path: '/__sample', name: 'sample', component: lazyRoute(() => import('./sample/SampleApp.vue')) })
  }
  routes.push({
    path: '/:unknown(.*)*',
    name: 'unknown',
    redirect: (to) => {
      o.onUnknownPath?.(to.path)
      return { path: '/', query: to.query, hash: '' }
    },
  })
  return createRouter({ history: o.history ?? createWebHistory(), routes })
}
