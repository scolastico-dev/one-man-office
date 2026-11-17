<script setup lang="ts">
// The stored MCP servers, one row each: name, live state, what it is, who
// gets it and, while it runs, how many tools it offers; a failure shows the
// relay's cleaned message (also while omo still retries it), a server
// without a sign-in says it needs one.
// Names and messages can come from files anyone with access to omo's home
// can write, so they are shown as visible text.
import { useId } from 'vue'
import type { MCPServer, MCPServerState } from '../../../api/types'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { reachWords, stateWords, toolWords, transportWords, unknownState } from './mcpForms'

const props = defineProps<{
  servers: readonly MCPServer[]
  states: readonly MCPServerState[]
  /** The server whose form is open. */
  selected: string | null
  /** The id of the open form, for aria-controls. */
  controls?: string
}>()
const emit = defineEmits<{ select: [name: string] }>()

const stateOf = (name: string): MCPServerState | null => props.states.find((s) => s.name === name) ?? null
/** Why a server that does not run failed last: also between two attempts, not only once omo gave up. */
const failureOf = (name: string): string => {
  const st = stateOf(name)
  return st !== null && st.state !== 'running' && st.state !== 'disabled' ? st.last_error : ''
}
const words = (name: string) => stateWords((stateOf(name) ?? unknownState(name)).state)
const fallback = useId()
</script>

<template>
  <ul class="mcp-list" data-mcp-server-list>
    <li v-for="s in servers" :key="s.name" class="mcp-list__row" :class="{ 'mcp-list__row--open': selected === s.name }" :data-mcp-server="s.name">
      <div class="mcp-list__main">
        <p class="mcp-list__name">
          <span>{{ visibleText(s.name) }}</span>
          <StatusBadge v-if="stateOf(s.name)" :tone="words(s.name).tone" :text="words(s.name).text" data-mcp-server-state />
          <StatusBadge v-if="!s.enabled && stateOf(s.name)?.state !== 'disabled'" tone="muted" text="off" />
          <StatusBadge v-if="s.global" tone="accent" text="also without session" title="Also available without an omo session" data-mcp-server-global />
        </p>
        <p v-if="s.description" class="mcp-list__meta">{{ visibleText(s.description) }}</p>
        <p class="mcp-list__meta" data-mcp-server-what>{{ visibleText(transportWords(s)) }} · {{ visibleText(reachWords(s)) }}</p>
        <p v-if="stateOf(s.name)?.state === 'running'" class="mcp-list__meta" data-mcp-server-tools>{{ toolWords(stateOf(s.name)!) }}</p>
        <p v-if="stateOf(s.name)?.state === 'needs_login'" class="mcp-list__warn" data-mcp-server-login>
          omo is not signed in at this server; Connect in its form signs in.
        </p>
        <p v-if="failureOf(s.name)" class="mcp-list__error" data-mcp-server-failure>
          {{ visibleText(failureOf(s.name)) }}
        </p>
      </div>
      <UiButton
        size="sm"
        :aria-expanded="selected === s.name ? 'true' : 'false'"
        :aria-controls="selected === s.name ? (controls ?? fallback) : undefined"
        data-mcp-server-toggle
        @click="emit('select', s.name)"
      >
        {{ selected === s.name ? 'Close' : 'Edit' }}<span class="sr-only"> {{ visibleText(s.name) }}</span>
      </UiButton>
    </li>
  </ul>
</template>

<style scoped>
.mcp-list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.mcp-list__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
}
.mcp-list__row--open {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.mcp-list__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.mcp-list__row p {
  margin: 0;
  overflow-wrap: anywhere;
}
.mcp-list__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.mcp-list__meta,
.mcp-list__warn,
.mcp-list__error {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.mcp-list__warn {
  color: var(--omo-warn);
}
.mcp-list__error {
  color: var(--omo-danger);
}
.mcp-list__row > :deep(button) {
  flex-shrink: 0;
}
</style>
