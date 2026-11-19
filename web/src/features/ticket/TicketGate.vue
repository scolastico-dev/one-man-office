<script setup lang="ts">
// The plugin gate of a ticket: every plugin's state, a hold's reason and the
// choices it offers (each runs the plugin's manual hook for the ticket, as
// you, through the same path as a plugin's ticket action), and, when the last
// move was refused, the plugin's reason. Continue anyway and Cancel move are
// actions of the row below (gateActions.ts). Everything a plugin wrote is
// shown as visible text.
import { computed, ref, watch } from 'vue'
import { failureText } from '../../api/lostAnswer'
import type { GateChoice, GateState, TicketGate, TicketSummary } from '../../api/types'
import { holdSummary } from '../../board/gates'
import type { DialogScope } from '../../components/ui/mountDialog'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { COLUMN_TITLES } from '../board/model'
import { useGateNotes } from './gateNotes'
import { runPluginHook } from './pluginActions'

const props = defineProps<{ ticket: TicketSummary; dialogs: DialogScope }>()

const session = useSession()
const notes = useGateNotes()

const STATE_WORDS: Record<GateState, string> = { pending: 'has not answered yet', held: 'holds the move', passed: 'gave its go-ahead' }

const gate = computed(() => props.ticket.gate)
const summary = computed(() => holdSummary(gate.value))
const move = computed(() => {
  const g = gate.value
  return g === null ? '' : `${COLUMN_TITLES[g.from]} to ${COLUMN_TITLES[g.to]}`
})
const refusal = computed(() => (gate.value === null ? notes.refusal(props.ticket.number) : undefined))

/** The choice whose hook runs now, as `plugin/action`. */
const running = ref<string | null>(null)
const error = ref('')
watch(
  () => props.ticket.number,
  () => {
    error.value = ''
  },
)

const key = (g: TicketGate, c: GateChoice) => `${g.plugin}/${c.action}`

async function choose(g: TicketGate, c: GateChoice) {
  if (running.value !== null) return
  error.value = ''
  running.value = key(g, c)
  try {
    const out = await runPluginHook(session.client, { plugin: g.plugin, action: c.action }, `${visibleText(g.plugin)}: ${visibleText(c.label)}`, props.ticket.number, props.dialogs, () => session.phase.value === 'ready')
    if (out === 'unknown') error.value = `No answer from omo, so ${visibleText(c.label)} may have run. The panel shows what omo did.`
  } catch (e) {
    error.value = failureText(e, { lost: 'No answer from omo.', refused: 'omo refused it.' })
  } finally {
    running.value = null
  }
}
</script>

<template>
  <section v-if="gate" class="gate" :data-gate-held="summary?.held ? '' : undefined" data-gate aria-label="Plugin gate">
    <p class="gate__head" data-gate-head>
      {{ summary?.held ? 'Held by a plugin' : 'Waiting for plugins' }}: {{ move }}
    </p>
    <ul class="gate__list">
      <li v-for="g in gate.gates" :key="g.plugin" class="gate__item" :data-gate-plugin="g.plugin">
        <p class="gate__line">
          <strong>{{ visibleText(g.plugin) }}</strong> <span data-gate-state>{{ STATE_WORDS[g.state] }}</span>
        </p>
        <p v-if="g.state === 'held' && g.reason" class="gate__reason" data-gate-reason>{{ visibleText(g.reason) }}</p>
        <div v-if="g.state === 'held' && g.choices.length > 0" class="gate__choices">
          <UiButton
            v-for="c in g.choices"
            :key="c.action"
            size="sm"
            :disabled="running !== null"
            :data-gate-choice="c.action"
            @click="choose(g, c)"
          >
            {{ running === key(g, c) ? `${visibleText(c.label)}…` : visibleText(c.label) }}
          </UiButton>
        </div>
      </li>
    </ul>
    <p v-if="error" class="gate__error" data-gate-error>{{ error }}</p>
  </section>
  <section v-else-if="refusal" class="gate gate--refused" data-gate-refusal aria-label="Refused move">
    <p class="gate__head">{{ visibleText(refusal.plugin) }} refused the move</p>
    <p class="gate__reason" data-gate-refusal-reason>{{ visibleText(refusal.reason) }}</p>
  </section>
</template>

<style scoped>
.gate {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.gate--refused {
  border-color: var(--omo-danger);
  background: var(--omo-danger-soft);
}
.gate__head {
  margin: 0;
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.gate__list {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.gate__item {
  display: grid;
  gap: var(--omo-space);
}
.gate__line,
.gate__reason,
.gate__error {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.gate__reason {
  white-space: pre-wrap;
}
.gate__error {
  color: var(--omo-danger);
}
.gate__choices {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
</style>
