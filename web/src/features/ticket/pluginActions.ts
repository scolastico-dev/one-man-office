// The installed plugins as the app knows them, shared per board store, and
// the plugins' ticket actions in every ticket panel's action row.
//
// The list is read once when it is first needed and stays current: a
// `plugin.changed` event carries one plugin's new state, a change of a plugin
// settings file (through omo or on disk), a snapshot and a reconnect read the
// whole list again. Reads are numbered, so an older answer never replaces a
// newer one, and a plugin's state from an event is kept over a read that
// started before it. Nothing here imports settings code: the ticket panel is
// part of the board's first chunk.
//
// A ticket action ("<plugin>: <description>") exists for every manual hook of
// scope `ticket` of a plugin that is installed, enabled and loaded. Pressing
// it asks nothing: it runs the hook for the ticket, as the user, and shows the
// answer in a small dialog over the panel (the action row shows a refusal
// under the button). Plugin names and texts are shown as visible text.
import { createApp, defineComponent, effectScope, h, shallowRef, watch, type App, type Ref } from 'vue'
import type { ApiClient } from '../../api/client'
import { getPlugins, runPluginAction } from '../../api/endpoints'
import { isLostAnswer } from '../../api/lostAnswer'
import type { ApiEvent, PluginInfo, PluginsResponse, SettingsChangedData, SettingsInvalidData } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import type { BoardStore } from '../../store'
import { registerTicketAction, type TicketAction } from './actions'

export interface PluginList {
  /** The last answer of `GET /api/plugins`, with newer plugin states from events; null until the first read. */
  data: Readonly<Ref<PluginsResponse | null>>
  /** Why the list could not be read, in the server's words; empty when it could. */
  error: Readonly<Ref<string>>
  /** Reads the list again; resolves once the answer is applied (or found old). */
  reload(): Promise<void>
  /** A plugin's state the server answered with (an install, an update, a change): newer than every read under way. */
  take(info: PluginInfo): void
}

export interface PluginListDeps {
  client: ApiClient
  store: Pick<BoardStore, 'onEvent' | 'onSnapshot' | 'connection'>
}

function isInfo(v: unknown): v is PluginInfo {
  return typeof v === 'object' && v !== null && typeof (v as PluginInfo).name === 'string' && Array.isArray((v as PluginInfo).hooks)
}

/** Whether an event may have changed the plugin settings files. */
function concernsSettings(ev: ApiEvent): boolean {
  if (ev.kind === 'settings.changed') return (ev.data as Partial<SettingsChangedData> | null)?.section === 'plugins'
  if (ev.kind === 'settings.invalid') {
    const f = (ev.data as Partial<SettingsInvalidData> | null)?.file
    return typeof f === 'string' && /[\\/]plugins[\\/][^\\/]+\.ya?ml$/.test(f)
  }
  return false
}

/** The live list for one store. Exported for tests; the app uses `usePluginList`. */
export function createPluginList(deps: PluginListDeps): PluginList {
  const { client, store } = deps
  const data = shallowRef<PluginsResponse | null>(null)
  const error = shallowRef('')

  let seq = 0
  let applied = 0
  /** Plugin states from events or answers, with the number of the newest read that started before them. */
  const newer = new Map<string, { info: PluginInfo; after: number }>()

  function withNewer(list: PluginInfo[], mine: number): PluginInfo[] {
    for (const [name, n] of newer) if (n.after < mine) newer.delete(name)
    return list.map((p) => newer.get(p.name)?.info ?? p)
  }

  async function read() {
    const mine = ++seq
    try {
      const { data: got } = await getPlugins(client)
      if (mine <= applied) return
      applied = mine
      data.value = { ...got, plugins: withNewer(got.plugins ?? [], mine) }
      error.value = ''
    } catch (e) {
      if (mine <= applied) return
      error.value = e instanceof Error && e.message !== '' ? e.message : 'The plugins could not be read.'
    }
  }

  function take(info: PluginInfo) {
    const now = data.value
    newer.set(info.name, { info, after: seq })
    if (now === null || !now.plugins.some((p) => p.name === info.name)) {
      // A plugin the list does not hold yet: its settings come with a read.
      void read()
      return
    }
    data.value = { ...now, plugins: now.plugins.map((p) => (p.name === info.name ? info : p)) }
  }

  const scope = effectScope(true)
  scope.run(() => {
    store.onEvent('*', (ev) => {
      if (ev.kind === 'plugin.changed') {
        if (isInfo(ev.data)) take(ev.data)
      } else if (concernsSettings(ev)) void read()
    })
    store.onSnapshot(() => void read())
    watch(
      () => store.connection.value,
      (now, before) => {
        if (now === 'live' && before !== 'live') void read()
      },
    )
  })
  void read()

  return { data, error, reload: read, take }
}

const lists = new WeakMap<object, PluginList>()

/** The installed plugins, shared by every caller of the same session; read on first use, then kept current. */
export function usePluginList(): PluginList {
  const session = useSession()
  let l = lists.get(session.store)
  if (!l) {
    l = createPluginList({ client: session.client, store: session.store })
    lists.set(session.store, l)
  }
  return l
}

// Ticket actions

/** One ticket action of a plugin, as the action row shows it. */
export interface PluginTicketAction {
  plugin: string
  action: string
  description: string
}

const RUNS_HOOKS = new Set(['ready', 'running', 'error'])

/** The ticket actions the list offers: manual hooks of scope ticket of plugins that are enabled and loaded, in the list's order. */
export function ticketActionsOf(d: PluginsResponse | null): PluginTicketAction[] {
  if (d === null) return []
  const out: PluginTicketAction[] = []
  for (const p of d.plugins) {
    if (!RUNS_HOOKS.has(p.state)) continue
    if (d.settings.find((s) => s.name === p.name)?.enabled === false) continue
    for (const hk of p.hooks) {
      if (hk.event === 'manual' && hk.scope === 'ticket' && hk.name !== '') out.push({ plugin: p.name, action: hk.name, description: hk.description })
    }
  }
  return out
}

/** The button's text: "<plugin>: <description>", as visible text. */
export const actionLabel = (a: PluginTicketAction) => `${visibleText(a.plugin)}: ${visibleText(a.description)}`

/** Shows the hook's answer over the panel until the user closes it. */
function showAnswer(title: string, n: number, text: string): void {
  const host = document.createElement('div')
  document.body.appendChild(host)
  let app: App | null = null
  const close = () => {
    const a = app
    if (a === null) return
    app = null
    a.unmount()
    host.remove()
  }
  const Answer = defineComponent({
    setup() {
      return () =>
        h(
          UiDialog,
          { open: true, title, eyebrow: `#${n}`, onClose: close, 'data-plugin-answer-dialog': '' },
          {
            default: () => [h('p', { class: 'm-0 break-words', role: 'status', 'data-plugin-answer': '' }, text === '' ? 'Done; the plugin gave no answer.' : visibleText(text))],
            actions: () => [h(UiButton, { variant: 'primary', 'data-primary': '', onClick: close, 'data-plugin-answer-close': '' }, () => 'Close')],
          },
        )
    },
  })
  app = createApp(Answer)
  app.mount(host)
}

/** The registered action for one plugin hook; it applies only in panels of `store`. */
export function pluginTicketAction(a: PluginTicketAction, store: object): TicketAction {
  const label = actionLabel(a)
  return {
    id: `plugin:${a.plugin}:${a.action}`,
    label,
    applies: (_t, ctx) => ctx.store === store,
    describe: (t) => `Runs the action of the plugin ${visibleText(a.plugin)} for #${t.number}; the plugin runs as you.`,
    run: async (t, ctx) => {
      let answer: string
      try {
        answer = (await runPluginAction(ctx.client, a.plugin, a.action, t.number)).data?.message ?? ''
      } catch (e) {
        // No answer: the action may have run; the plugin's log says.
        if (isLostAnswer(e)) return 'unknown'
        throw e
      }
      showAnswer(label, t.number, answer)
    },
  }
}

const registered = new WeakMap<object, true>()

/**
 * Keeps one ticket action per plugin ticket action of the session's list in
 * the action registry, for as long as the app runs; called by the ticket
 * panel. Calling it again changes nothing.
 */
export function usePluginActions(): void {
  const session = useSession()
  const store = session.store
  if (registered.has(store)) return
  registered.set(store, true)
  const list = usePluginList()
  let key = ''
  let off: (() => void)[] = []
  effectScope(true).run(() => {
    watch(
      () => ticketActionsOf(list.data.value),
      (now) => {
        const next = JSON.stringify(now)
        if (next === key) return
        key = next
        for (const f of off) f()
        off = now.map((a) => registerTicketAction(pluginTicketAction(a, store)))
      },
      { immediate: true },
    )
  })
}
