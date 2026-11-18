// Each classic entry captures document.currentScript.omo before awaiting.
// No mutable window-global facade and no eval/module/blob script loader.
import { effectScope, shallowRef, watch, type Ref } from 'vue'
import type { ApiClient } from '../../api/client'
import { runPluginAction } from '../../api/endpoints'
import type { PluginInfo, PluginRunResult, PluginsResponse } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'
import { navigatePluginResult } from '../proxy/pluginNavigation'

export interface PluginContext {
  page: 'board' | 'ticket' | 'settings' | 'terminal'
  ticket?: { number: number; title: string; projects: { id: string; worktree: string }[]; workdir: string }
  project?: string
}
export interface HookContext { ticket?: number; project?: string }
export interface ScriptButton {
  id: string
  label: string
  place: 'ticket' | 'sidebar' | 'project'
  per_worktree?: boolean
  action?: string
  run?: (context: PluginContext) => PluginRunResult | void | Promise<PluginRunResult | void>
}
export interface ScriptPanel {
  id: string
  place: 'ticket' | 'sidebar'
  title: string
  text: string
  image?: string
}
export interface PluginFacade {
  readonly signal: AbortSignal
  initialize(setup: (omo: PluginFacade) => void | (() => void) | Promise<void | (() => void)>): void
  addButton(button: ScriptButton): () => void
  removeButton(id: string): void
  addPanel(panel: ScriptPanel): () => void
  removePanel(id: string): void
  context(): PluginContext
  onRouteChange(callback: (context: PluginContext) => void | Promise<void>): () => void
  runHook(action: string, context?: HookContext): Promise<PluginRunResult>
  assetUrl(path: string): string
  openForward(id: string): Promise<void>
  open(action: () => Promise<PluginRunResult>): Promise<PluginRunResult>
}
export interface OwnedButton extends ScriptButton { plugin: string; active(): boolean; invoke(context: PluginContext): Promise<PluginRunResult> }
export interface OwnedPanel extends ScriptPanel { plugin: string }
interface Lifetime {
  identity: string
  script: HTMLScriptElement
  controller: AbortController
  subscriptions: Set<(context: PluginContext) => void | Promise<void>>
  cleanup?: () => void
  timer?: ReturnType<typeof setTimeout>
  removeErrorListener?: () => void
  initialized: boolean
  failed: boolean
}
export function enabledPlugins(list: PluginsResponse | null): PluginInfo[] {
  return (list?.plugins ?? []).filter((p) => ['ready', 'running', 'error'].includes(p.state) && list?.settings.find((s) => s.name === p.name)?.enabled !== false)
}
export const pluginCodeIdentity = (info: PluginInfo): string => [info.content_id, info.commit, info.version].join(':')
function portablePath(path: string): boolean {
  if (!path || new TextEncoder().encode(path).length > 512 || visibleText(path) !== path || /[\\:<>"|?*]/.test(path)) return false
  return path.split('/').every((part) => {
    if (!part || part.startsWith('.') || /[. ]$/.test(part) || /^(settings(\.ya?ml|\.json)?|plugin\.json)$/i.test(part)) return false
    const stem = part.split('.')[0]!.toUpperCase()
    return !/^(CON|PRN|AUX|NUL|CONIN\$|CONOUT\$|COM[1-9¹²³]|LPT[1-9¹²³])$/.test(stem)
  })
}
const copyContext = (context: PluginContext): PluginContext => JSON.parse(JSON.stringify(context)) as PluginContext
const labelValid = (text: string) => typeof text === 'string' && text.trim() !== '' && text.length <= 200 && visibleText(text) === text && !/[\r\n]/.test(text)

export function createFrontendRuntime(deps: { list: Readonly<Ref<PluginsResponse | null>>; context: Readonly<Ref<PluginContext>>; suspended: Readonly<Ref<boolean>>; client: ApiClient }) {
  const buttons = shallowRef<OwnedButton[]>([])
  const panels = shallowRef<OwnedPanel[]>([])
  const errors = shallowRef<{ plugin: string; message: string }[]>([])
  const lives = new Map<string, Lifetime>()
  const scope = effectScope(true)
  let disposed = false

  function clear(name: string, life: Lifetime) {
    life.controller.abort()
    clearTimeout(life.timer)
    life.script.remove()
    life.removeErrorListener?.()
    life.subscriptions.clear()
    buttons.value = buttons.value.filter((b) => b.plugin !== name)
    panels.value = panels.value.filter((p) => p.plugin !== name)
    try { life.cleanup?.() } catch { /* Cleanup cannot prevent other plugins from being removed. */ }
    life.cleanup = undefined
  }
  function fail(name: string, life: Lifetime, message: string) {
    if (life.controller.signal.aborted) return
    life.failed = true
    clear(name, life)
    errors.value = [...errors.value.filter((e) => e.plugin !== name), { plugin: name, message: visibleText(message).slice(0, 1000) }]
  }
  function notifyRoute(name: string, life: Lifetime, callback: (context: PluginContext) => void | Promise<void>) {
    if (life.controller.signal.aborted || deps.suspended.value || disposed) return
    const failed = () => fail(name, life, 'A plugin route listener failed. Retry the extension.')
    try { Promise.resolve(callback(copyContext(deps.context.value))).catch(failed) } catch { failed() }
  }
  function start(info: PluginInfo) {
    const name = info.name
    const script = document.createElement('script')
    script.src = `/plugin-assets/${encodeURIComponent(name)}/${encodeURIComponent(info.content_id)}/${info.frontend.script!.split('/').map(encodeURIComponent).join('/')}`
    script.dataset.omoPlugin = name
    const life: Lifetime = { identity: pluginCodeIdentity(info), script, controller: new AbortController(), subscriptions: new Set(), initialized: false, failed: false }
    lives.set(name, life)
    const active = () => !disposed && lives.get(name) === life && !life.controller.signal.aborted
    const check = () => { if (!active()) throw new Error('The plugin is no longer active.') }
    const actionable = () => { check(); if (deps.suspended.value) throw new Error('The board is locked.') }
    const notify = (callback: (c: PluginContext) => void | Promise<void>) => notifyRoute(name, life, callback)
    const runHook = async (action: string, context?: HookContext) => {
      actionable()
      const hook = info.hooks.find((h) => h.event === 'manual' && h.name === action)
      if (!hook) throw new Error('Only this plugin’s own manual hooks may be invoked.')
      const selected = context ?? (hook.scope === 'ticket' ? { ticket: deps.context.value.ticket?.number, project: deps.context.value.project } : { project: deps.context.value.project })
      if (hook.scope === 'ticket' && !selected.ticket) throw new Error('This manual hook needs a ticket.')
      const result = (await runPluginAction(deps.client, name, action, selected.ticket, selected.project)).data
      actionable()
      return result
    }
    const omo: PluginFacade = Object.freeze({
      signal: life.controller.signal,
      initialize(setup: Parameters<PluginFacade['initialize']>[0]) {
        check()
        if (life.initialized) throw new Error('The plugin was already initialized.')
        life.initialized = true
        Promise.resolve().then(() => { check(); return setup(omo) }).then((cleanup) => {
          if (typeof cleanup !== 'function') return
          if (active()) life.cleanup = cleanup
          else { try { cleanup() } catch { /* Obsolete initialization cleanup. */ } }
        }).catch(() => fail(name, life, 'The plugin frontend initialization failed. Retry the extension.'))
      },
      addButton(button: ScriptButton) {
        check()
        if (!/^[\w.-]{1,64}$/.test(button.id) || !labelValid(button.label) || !['ticket', 'sidebar', 'project'].includes(button.place) || (!!button.action === (typeof button.run === 'function')) || (button.per_worktree && button.place !== 'ticket')) throw new Error('Invalid plugin button.')
        if (buttons.value.filter((b) => b.plugin === name && b.id !== button.id).length >= 64) throw new Error('Too many plugin buttons.')
        const registered: OwnedButton = { ...button, plugin: name, active, async invoke(context) {
          actionable()
          return (button.run ? await button.run(copyContext(context)) : await runHook(button.action!, { ticket: context.ticket?.number, project: context.project })) ?? { message: '' }
        } }
        buttons.value = [...buttons.value.filter((b) => b.plugin !== name || b.id !== button.id), registered]
        return () => { buttons.value = buttons.value.filter((b) => b !== registered) }
      },
      removeButton(id: string) { check(); buttons.value = buttons.value.filter((b) => b.plugin !== name || b.id !== id) },
      addPanel(panel: ScriptPanel) {
        check()
        if (!/^[\w.-]{1,64}$/.test(panel.id) || !labelValid(panel.title) || typeof panel.text !== 'string' || panel.text.length > 16000 || !['ticket', 'sidebar'].includes(panel.place) || (panel.image && !panel.image.startsWith(`/plugin-assets/${encodeURIComponent(name)}/${info.content_id}/`))) throw new Error('Invalid plugin panel.')
        if (panels.value.filter((p) => p.plugin === name && p.id !== panel.id).length >= 64) throw new Error('Too many plugin panels.')
        const registered: OwnedPanel = { ...panel, plugin: name }
        panels.value = [...panels.value.filter((p) => p.plugin !== name || p.id !== panel.id), registered]
        return () => { panels.value = panels.value.filter((p) => p !== registered) }
      },
      removePanel(id: string) { check(); panels.value = panels.value.filter((p) => p.plugin !== name || p.id !== id) },
      context() { check(); return copyContext(deps.context.value) },
      onRouteChange(callback: (context: PluginContext) => void | Promise<void>) { check(); life.subscriptions.add(callback); notify(callback); return () => life.subscriptions.delete(callback) },
      runHook,
      assetUrl(path: string) {
        check()
        if (!info.frontend.assets || !portablePath(path)) throw new Error('Invalid plugin asset path.')
        return `/plugin-assets/${encodeURIComponent(name)}/${info.content_id}/${info.frontend.assets.split('/').map(encodeURIComponent).join('/')}/${path.split('/').map(encodeURIComponent).join('/')}`
      },
      async openForward(id: string) { actionable(); await navigatePluginResult(deps.client, async () => ({ message: '', forward_id: id }), () => active() && !deps.suspended.value) },
      async open(action: () => Promise<PluginRunResult>) { actionable(); return navigatePluginResult(deps.client, action, () => active() && !deps.suspended.value) },
    })
    Object.defineProperty(script, 'omo', { value: omo })
    script.onload = () => { clearTimeout(life.timer); if (!life.initialized) fail(name, life, 'The plugin entry did not call omo.initialize. Retry the extension.') }
    script.onerror = () => fail(name, life, 'The plugin frontend could not load. Retry the extension.')
    const scriptError = (event: ErrorEvent) => {
      if (event.filename === script.src) fail(name, life, 'The plugin frontend script failed. Retry the extension.')
    }
    window.addEventListener('error', scriptError)
    life.removeErrorListener = () => window.removeEventListener('error', scriptError)
    life.timer = setTimeout(() => fail(name, life, 'The plugin frontend load timed out. Retry the extension.'), 15000)
    document.head.append(script)
  }
  function reconcile() {
    const infos = enabledPlugins(deps.list.value).filter((p) => p.frontend?.script && p.content_id)
    for (const [name, life] of lives) {
      if (!infos.some((p) => p.name === name && pluginCodeIdentity(p) === life.identity)) {
        clear(name, life)
        lives.delete(name)
        errors.value = errors.value.filter((e) => e.plugin !== name)
      }
    }
    if (deps.suspended.value || disposed) return
    for (const info of infos) if (!lives.has(info.name)) start(info)
  }
  scope.run(() => {
    watch([deps.list, deps.suspended], reconcile, { immediate: true, flush: 'sync' })
    watch([deps.context, deps.suspended], () => {
      if (deps.suspended.value) return
      for (const [name, life] of lives) for (const callback of [...life.subscriptions]) {
        if (life.controller.signal.aborted) break
        notifyRoute(name, life, callback)
      }
    })
  })
  return {
    buttons, panels, errors,
    retry(name: string) {
      const life = lives.get(name)
      if (!life?.failed) return
      clear(name, life); lives.delete(name)
      errors.value = errors.value.filter((e) => e.plugin !== name)
      reconcile()
    },
    dispose() {
      disposed = true; scope.stop()
      for (const [name, life] of lives) clear(name, life)
      lives.clear(); errors.value = []
    },
  }
}
export type FrontendRuntime = ReturnType<typeof createFrontendRuntime>
