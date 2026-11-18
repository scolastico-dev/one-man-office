<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { runPluginAction } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { PluginButton, TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { navigatePluginResult } from '../proxy/pluginNavigation'
import { controlsOf, type PluginControl } from './controls'
import { enabledPlugins, pluginCodeIdentity } from './frontendRuntime'
import { ticketContext, usePluginExtensions } from './usePluginExtensions'

const props = defineProps<{ place: PluginButton['place']; ticket?: TicketSummary; project?: string }>()
const session = useSession()
const { list, runtime, context, suspended } = usePluginExtensions()
const buttons = computed(() => controlsOf(list.data.value, runtime.buttons.value, props.place, props.ticket, props.project))
const panels = computed(() => runtime.panels.value.filter((p) => p.place === props.place))
const running = ref<string | null>(null)
const answer = ref('')
const error = ref('')
watch(() => [props.ticket?.number, props.project], () => { answer.value = ''; error.value = '' })
async function run(button: PluginControl) {
  if (running.value || suspended.value || button.why) return
  running.value = button.id; answer.value = ''; error.value = ''
  const nextContext = { ...context.value, ...(props.ticket ? { ticket: ticketContext(props.ticket) } : { ticket: undefined }), project: button.project }
  const plugin = enabledPlugins(list.data.value).find((p) => p.name === button.plugin)
  const identity = plugin ? pluginCodeIdentity(plugin) : undefined
  const active = () => !suspended.value && (button.scripted ? button.scripted.active() : enabledPlugins(list.data.value).some((p) => p.name === button.plugin && pluginCodeIdentity(p) === identity))
  try {
    const result = await navigatePluginResult(session.client, async () => button.scripted ? button.scripted.invoke(nextContext) : (await runPluginAction(session.client, button.plugin, button.action!, button.ticket, button.project)).data, active)
    answer.value = result.message || 'Done; the plugin gave no message.'
  } catch (e) {
    error.value = failureText(e, { lost: 'No answer from omo; the action may have run. Check the plugin log before trying again.', refused: 'The plugin action failed.' })
  } finally { running.value = null }
}
</script>
<template>
  <div v-if="buttons.length || panels.length" class="plugin-controls" :data-plugin-controls="place">
    <div v-for="b in buttons" :key="b.id" class="plugin-controls__button">
      <UiButton size="sm" :disabled="!!b.why || !!running || suspended" :aria-busy="running === b.id ? 'true' : undefined" :title="b.why" :data-plugin-control="b.id" @click="run(b)">{{ visibleText(b.label) }}{{ running === b.id ? '…' : '' }}</UiButton>
      <p v-if="b.why" class="plugin-controls__text">{{ b.why }}</p>
    </div>
    <section v-for="p in panels" :key="`${p.plugin}:${p.id}`" class="plugin-controls__panel" :aria-label="visibleText(p.title)" :data-plugin-panel="`${p.plugin}:${p.id}`">
      <h3>{{ visibleText(p.title) }}</h3>
      <p class="plugin-controls__text">{{ visibleText(p.text) }}</p>
      <img v-if="p.image" :src="p.image" :alt="visibleText(p.title)" />
    </section>
    <p v-if="answer" class="plugin-controls__text" role="status" data-plugin-control-answer>{{ visibleText(answer) }}</p>
    <p v-if="error" class="plugin-controls__error" role="alert" data-plugin-control-error>{{ visibleText(error) }}</p>
  </div>
</template>
<style scoped>
.plugin-controls { display: flex; flex-wrap: wrap; gap: calc(var(--omo-space) * 2); min-width: 0; }
.plugin-controls__button { display: grid; gap: var(--omo-space); max-width: 100%; }
.plugin-controls__text, .plugin-controls__error { margin: 0; font-size: var(--omo-text-sm); line-height: var(--omo-text-sm-lh); white-space: pre-wrap; overflow-wrap: anywhere; }
.plugin-controls__error { color: var(--omo-danger); }
.plugin-controls__panel { flex: 1 1 100%; min-width: 0; padding: calc(var(--omo-space) * 2); border: var(--omo-border) solid var(--omo-line); }
.plugin-controls__panel h3 { margin: 0 0 var(--omo-space); font-size: var(--omo-text-sm); }
.plugin-controls__panel img { max-width: 100%; max-height: calc(var(--omo-space) * 50); object-fit: contain; }
</style>
