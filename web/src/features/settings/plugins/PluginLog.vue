<script setup lang="ts">
// A plugin's log: the newest 200 lines, oldest first, as plain text in a box
// that scrolls by itself. The lines are what the plugin wrote (cleaned by
// omo), so they are shown as visible lines, never as markup. Read when shown,
// on Refresh and when the plugin has run again; reads are numbered.
import { computed, ref, watch } from 'vue'
import { getPluginLog } from '../../../api/endpoints'
import type { PluginLogLine } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleLines, visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { logTime } from './pluginForms'

const props = defineProps<{
  name: string
  /** When the plugin last ran: a new value reads the log again. */
  lastRunAt?: string | null
}>()

const session = useSession()
const lines = ref<PluginLogLine[] | null>(null)
const error = ref('')
const reading = ref(false)

let seq = 0
let applied = 0
async function read() {
  const mine = ++seq
  reading.value = true
  try {
    const { data } = await getPluginLog(session.client, props.name)
    if (mine <= applied) return
    applied = mine
    lines.value = data ?? []
    error.value = ''
  } catch (e) {
    if (mine <= applied) return
    error.value = e instanceof Error && e.message !== '' ? e.message : 'The log could not be read.'
  } finally {
    if (mine === seq) reading.value = false
  }
}
watch(() => [props.name, props.lastRunAt], () => void read(), { immediate: true })

const text = computed(() => visibleLines((lines.value ?? []).map((l) => `${logTime(l.at)}  ${l.message.replace(/\n/g, ' ')}`).join('\n')))
const label = computed(() => `Log of ${visibleText(props.name)}`)
</script>

<template>
  <div class="plugin-log" :data-plugin-log="name">
    <div class="plugin-log__bar">
      <p class="plugin-log__label">Log, newest last</p>
      <UiButton size="sm" variant="ghost" :aria-busy="reading ? 'true' : undefined" data-plugin-log-refresh @click="read()">
        Refresh<span class="sr-only"> the log of {{ visibleText(name) }}</span>
      </UiButton>
    </div>
    <p v-if="error" class="plugin-log__error" role="alert">{{ visibleText(error) }}</p>
    <p v-else-if="lines !== null && lines.length === 0" class="plugin-log__empty" data-plugin-log-empty>No log lines yet.</p>
    <pre v-if="lines !== null && lines.length > 0" class="omo-logtext plugin-log__text" tabindex="0" :aria-label="label" data-plugin-log-text>{{ text }}</pre>
  </div>
</template>

<style scoped>
.plugin-log {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.plugin-log__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  padding-right: calc(var(--omo-space) * 1.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.plugin-log__label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.plugin-log__empty,
.plugin-log__error {
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.plugin-log__error {
  color: var(--omo-danger);
  overflow-wrap: anywhere;
}
.plugin-log__text {
  max-height: calc(var(--omo-space) * 50);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.plugin-log__text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
</style>
