import { computed, onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import type { TicketSummary } from '../../api/types'
import { useRoute } from 'vue-router'
import { useSession } from '../../session'
import { usePluginList } from '../ticket/pluginActions'
import { createFrontendRuntime, type PluginContext } from './frontendRuntime'

export function ticketContext(ticket: { number: number; title: string; workdir: string; projects: { project: string; worktree: string }[] }): NonNullable<PluginContext['ticket']> {
  return { number: ticket.number, title: ticket.title, workdir: ticket.workdir, projects: ticket.projects.map((p) => ({ id: p.project, worktree: p.worktree })) }
}
const extensions = new WeakMap<object, ReturnType<typeof createExtensions>>()
function createExtensions() {
  const session = useSession()
  const route = useRoute()
  const list = usePluginList()
  const suspended = computed(() => session.phase.value !== 'ready')
  const viewedTicket = shallowRef<PluginContext['ticket'] | null>(null)
  const context = computed<PluginContext>(() => {
    const number = ['ticket', 'ticket-page'].includes(String(route.name)) ? Number(route.params.n) : undefined
    const ticket = number ? session.store.state.tickets.get(number)?.t : undefined
    const page = number ? 'ticket' : route.name === 'terminal' ? 'terminal' : route.name === 'settings' ? 'settings' : 'board'
    const selected = typeof route.query.project === 'string' && session.store.state.projects.has(route.query.project) ? route.query.project : undefined
    const currentTicket = ticket ? ticketContext(ticket) : viewedTicket.value?.number === number ? viewedTicket.value : undefined
    return { page, ...(currentTicket ? { ticket: currentTicket } : {}), ...(selected ? { project: selected } : {}) }
  })
  const runtime = createFrontendRuntime({ list: list.data, context, suspended, client: session.client })
  return { list, runtime, context, suspended, viewedTicket }
}
export function usePluginExtensions() {
  const store = useSession().store
  let value = extensions.get(store)
  if (!value) { value = createExtensions(); extensions.set(store, value) }
  return value
}
export function disposePluginExtensions(store: object) { extensions.get(store)?.runtime.dispose(); extensions.delete(store) }
/** Includes directly fetched tickets (such as old Done tickets outside the board snapshot). */
export function usePluginTicketContext(ticket: Readonly<Ref<TicketSummary | null>>) {
  const extensions = usePluginExtensions()
  let owned: PluginContext['ticket'] | null = null
  watch(ticket, (current) => {
    owned = current ? ticketContext(current) : null
    extensions.viewedTicket.value = owned
  }, { immediate: true })
  onBeforeUnmount(() => { if (extensions.viewedTicket.value === owned) extensions.viewedTicket.value = null })
}
