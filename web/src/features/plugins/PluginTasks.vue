<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { getPluginTasks, listProxyForwards, stopPluginTask } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { PluginTask, ProxyForward } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleLines, visibleText } from '../../components/ui/visibleText'
import { useNow } from '../../components/ui/useNow'
import { useSession } from '../../session'
import { openForward, openingForwards } from '../proxy/openForward'

const props = defineProps<{ name: string; runningOnly?: boolean }>()
const session = useSession()
const tasks = ref<PluginTask[] | null>(null)
const forwards = ref<ProxyForward[]>([])
const error = ref('')
const stopping = ref<string | null>(null)
const reading = ref(false)
const now = useNow()
const suspended = computed(() => session.phase.value !== 'ready')
const shown = computed(() => (tasks.value ?? []).filter(t => !props.runningOnly || t.state === 'running').map(task => {
  const forward = props.runningOnly && task.state === 'running' ? forwards.value.find(f => f.plugin === props.name && f.task_id === task.id && f.ticket === (task.ticket ?? null)) : undefined
  return { task, forward, label: forward?.name || task.name }
}))
let alive = true
let seq = 0
async function refresh() {
  if (suspended.value) return
  const mine = ++seq
  reading.value = true
  try {
    const [taskResult, forwardResult] = await Promise.allSettled([
      getPluginTasks(session.client, props.name), props.runningOnly ? listProxyForwards(session.client) : Promise.resolve(null),
    ])
    if (!alive || mine !== seq || suspended.value) return
    const problems: string[] = []
    if (taskResult.status === 'fulfilled') tasks.value = taskResult.value.data.tasks ?? []
    else problems.push(taskResult.reason instanceof Error ? taskResult.reason.message : 'The tasks could not be read.')
    if (forwardResult.status === 'fulfilled') forwards.value = forwardResult.value?.data.forwards ?? []
    else {
      forwards.value = []
      problems.push(forwardResult.reason instanceof Error ? forwardResult.reason.message : 'The instance links could not be read.')
    }
    error.value = problems.join(' ')
  } catch (e) {
    if (!alive || mine !== seq) return
    error.value = e instanceof Error ? e.message : 'The tasks could not be read.'
  } finally { if (alive && mine === seq) reading.value = false }
}
async function open(forward: ProxyForward) {
  if (suspended.value) return
  error.value = ''
  try {
    await openForward(session.client, forward.id)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'The instance could not be opened.'
  }
}
async function stop(task: PluginTask) {
  if (stopping.value || suspended.value) return
  stopping.value = task.id
  error.value = ''
  try {
    await stopPluginTask(session.client, props.name, task.name)
    await refresh()
  } catch (e) {
    error.value = failureText(e, { lost: 'No answer from omo; the task may have stopped. Refresh before trying again.', refused: 'The task could not be stopped.' })
  } finally { stopping.value = null }
}
function elapsed(task: PluginTask): string {
  const end = task.ended_at ? Date.parse(task.ended_at) : now.value
  const seconds = Math.max(0, Math.floor((end - Date.parse(task.started_at)) / 1000))
  if (!Number.isFinite(seconds)) return 'Time unavailable'
  return seconds < 60 ? `${seconds}s` : seconds < 3600 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${Math.floor(seconds / 3600)}h ${Math.floor(seconds / 60) % 60}m`
}
function output(task: PluginTask): string {
  return visibleLines((task.output ?? []).slice(-100).map((line) => Array.from(line).slice(0, 1024).join('')).join('\n'))
}
const offEvent = session.store.onEvent('*', (ev) => {
  const taskChanged = ev.kind === 'plugin.tasks_changed' && (ev.data as { name?: string })?.name === props.name
  const forwardChanged = props.runningOnly && (ev.kind === 'proxy.forward_added' || ev.kind === 'proxy.forward_updated' || ev.kind === 'proxy.forward_removed') && (ev.data as ProxyForward).plugin === props.name
  if (taskChanged || forwardChanged) void refresh()
})
const offSnapshot = session.store.onSnapshot(() => void refresh())
watch(() => session.store.connection.value, (current, previous) => { if (current === 'live' && previous !== 'live') void refresh() })
watch(() => [props.name, suspended.value], () => { if (!suspended.value) void refresh() }, { immediate: true })
onBeforeUnmount(() => { alive = false; seq++; offEvent(); offSnapshot() })
</script>
<template>
  <section class="plugin-tasks" :aria-label="`Tasks of ${visibleText(name)}`" :data-plugin-tasks="name">
    <div class="plugin-tasks__bar">
      <h4>{{ runningOnly ? 'Running instances' : 'Background tasks' }}</h4>
      <UiButton size="sm" :disabled="reading || suspended" :aria-busy="reading ? 'true' : undefined" @click="refresh">Refresh tasks</UiButton>
    </div>
    <p v-if="error" role="alert">{{ visibleText(error) }}</p>
    <p v-if="tasks === null">Reading tasks…</p>
    <p v-else-if="!shown.length">{{ runningOnly ? 'No running instances.' : 'No tasks.' }}</p>
    <article v-for="{ task, forward, label } in shown" :key="task.id" class="plugin-tasks__task" :aria-label="visibleText(label)">
      <div class="plugin-tasks__bar">
        <strong>{{ visibleText(label) }}</strong>
        <span v-if="task.ticket">Ticket #{{ task.ticket }}</span>
        <span data-task-elapsed>{{ elapsed(task) }}</span>
        <span>{{ visibleText(task.state) }}{{ task.reason ? ` · ${visibleText(task.reason)}` : '' }}</span>
        <UiButton v-if="forward" size="sm" :disabled="openingForwards.has(forward.id) || stopping === task.id || suspended" :aria-busy="openingForwards.has(forward.id) ? 'true' : undefined" data-task-open @click="open(forward)">Open<span class="sr-only"> {{ visibleText(label) }} in new tab</span></UiButton>
        <UiButton v-if="task.state === 'running'" size="sm" :disabled="!!stopping || suspended" :aria-busy="stopping === task.id ? 'true' : undefined" data-task-stop @click="stop(task)">Stop<span class="sr-only"> {{ visibleText(label) }}</span></UiButton>
      </div>
      <p v-if="task.error" role="status">{{ visibleText(task.error) }}</p>
      <pre v-if="!runningOnly && task.output?.length" class="omo-logtext" tabindex="0" :aria-label="`Output of ${visibleText(task.name)}`">{{ output(task) }}</pre>
      <p v-else-if="!runningOnly">No output yet.</p>
    </article>
  </section>
</template>
<style scoped>
.plugin-tasks { display: grid; gap: calc(var(--omo-space) * 2); min-width: 0; }
.plugin-tasks__bar { display: flex; flex-wrap: wrap; align-items: center; gap: calc(var(--omo-space) * 2); min-width: 0; }
.plugin-tasks h4, .plugin-tasks p { margin: 0; font-size: var(--omo-text-sm); line-height: var(--omo-text-sm-lh); overflow-wrap: anywhere; }
.plugin-tasks__task { display: grid; gap: calc(var(--omo-space) * 2); min-width: 0; padding: calc(var(--omo-space) * 2); border: var(--omo-border) solid var(--omo-line); font-size: var(--omo-text-sm); overflow-wrap: anywhere; }
.plugin-tasks pre { max-height: calc(var(--omo-space) * 60); max-width: 100%; overflow: auto; margin: 0; will-change: transform; }
</style>
