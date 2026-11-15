import { createRouter, createWebHistory, type RouteRecordRaw, type Router, type RouterHistory } from 'vue-router'

/** What the board shows lives in the app itself; routes only name the place. */
const Nothing = { render: () => null }

export interface AppRouterOptions {
  history?: RouterHistory
  /** Called with the path of an address that names nothing; the router then shows the board. */
  onUnknownPath?(path: string): void
}

/**
 * The app's addresses: `/` the board, `/<n>` the board with ticket #n open,
 * and, in development only, `/__sample` the sample board. Any other address
 * shows the board, keeping its query. No address ever carries the login token.
 */
export function createAppRouter(o: AppRouterOptions = {}): Router {
  const routes: RouteRecordRaw[] = [
    { path: '/', name: 'board', component: Nothing },
    { path: '/:n(\\d+)', name: 'ticket', component: Nothing },
  ]
  if (import.meta.env.DEV) {
    routes.push({ path: '/__sample', name: 'sample', component: () => import('./sample/SampleApp.vue') })
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
