<script setup lang="ts">
// An MCP server's log: the newest 200 lines, oldest first, as plain text in a box
// that scrolls by itself. The lines are the server's output and omo's notes about it (values of its env and headers redacted by
// omo), so they are shown as visible lines, never as markup. Read when shown,
// on Refresh and when its state changes; reads are numbered.
import { computed, ref, watch } from 'vue'
import { getMCPServerLog } from '../../../api/endpoints'
import type { MCPLogLine } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleLines, visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { logTime } from '../plugins/pluginForms'

const props = defineProps<{
  name: string
  /** A new value (the state changed) reads the log again. */
  changed?: string | null
}>()

const session = useSession()
const lines = ref<MCPLogLine[] | null>(null)
const error = ref('')
const reading = ref(false)

let seq = 0
let applied = 0
async function read() {
  const mine = ++seq
  reading.value = true
  try {
    const { data } = await getMCPServerLog(session.client, props.name)
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
watch(() => [props.name, props.changed], () => void read(), { immediate: true })

const text = computed(() => visibleLines((lines.value ?? []).map((l) => `${logTime(l.at)}  ${l.message.replace(/\n/g, ' ')}`).join('\n')))
const label = computed(() => `Log of ${visibleText(props.name)}`)
</script>

<template>
  <div class="mcp-log" :data-mcp-log="name">
    <div class="mcp-log__bar">
      <p class="mcp-log__label">Log, newest last</p>
      <UiButton size="sm" variant="ghost" :aria-busy="reading ? 'true' : undefined" data-mcp-log-refresh @click="read()">
        Refresh<span class="sr-only"> the log of {{ visibleText(name) }}</span>
      </UiButton>
    </div>
    <p v-if="error" class="mcp-log__error" role="alert">{{ visibleText(error) }}</p>
    <p v-else-if="lines !== null && lines.length === 0" class="mcp-log__empty" data-mcp-log-empty>No log lines yet.</p>
    <pre v-if="lines !== null && lines.length > 0" class="mcp-log__text" tabindex="0" :aria-label="label" data-mcp-log-text>{{ text }}</pre>
  </div>
</template>

<style scoped>
.mcp-log {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.mcp-log__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  padding-right: calc(var(--omo-space) * 1.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.mcp-log__label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.mcp-log__empty,
.mcp-log__error {
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.mcp-log__error {
  color: var(--omo-danger);
  overflow-wrap: anywhere;
}
.mcp-log__text {
  max-height: calc(var(--omo-space) * 50);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: pre;
}
.mcp-log__text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
</style>
