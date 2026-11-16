<script setup lang="ts">
// One live agent as a row: who it is and what state it is in, the ticket it
// works on, its project and model, and how long it has been running. The row
// is a button; choosing it asks to show the agent's terminal. Every name here
// is free text from outside, shown as text.
import { computed } from 'vue'
import LiveDot from '../../components/ui/LiveDot.vue'
import RelativeTime from '../../components/ui/RelativeTime.vue'
import { visibleText } from '../../components/ui/visibleText'
import type { AgentSession } from '../../api/types'
import { useBoard } from '../../store'
import { ROLE_LABELS } from '../board/model'
import { keptAgent } from '../ticket/agentActivity'
import { stateWord } from './useAgents'

const props = defineProps<{
  session: AgentSession
  /** Leaves out the ticket, project and model, for a list that is already about one ticket. */
  compact?: boolean
}>()
defineEmits<{ open: [session: AgentSession] }>()

const store = useBoard()

const state = computed(() => props.session.state as 'running' | 'starting' | 'waiting')
const word = computed(() => stateWord(props.session.state))
const role = computed(() => ROLE_LABELS[props.session.role] ?? visibleText(String(props.session.role)))
const ticket = computed(() => (props.session.ticket === null ? null : { n: props.session.ticket, title: store.view(props.session.ticket)?.title ?? '' }))
const projectName = computed(() => {
  const id = props.session.project
  return id === '' ? '' : (store.state.projects.get(id)?.name ?? id)
})
// A parked agent omo keeps for questions says so: a reviewer after its verdict, the working agent while its ticket is in review.
const kept = computed(() => {
  const n = props.session.ticket
  const t = n === null ? undefined : store.view(n)
  if (!t || !keptAgent(props.session, t)) return ''
  return props.session.role === 'reviewer'
    ? 'Available for questions about its review until the next review round.'
    : 'Available for questions while the ticket is reviewed.'
})
const meta = computed(() => {
  const parts = [projectName.value, props.session.model]
  if (props.session.purpose === 'export') parts.push('export job')
  if (props.session.review_round > 0) parts.push(`review round ${props.session.review_round}`)
  return parts.filter((p) => p !== '').map(visibleText).join(' · ')
})
</script>

<template>
  <button type="button" class="srow" :title="`Show the terminal of ${visibleText(session.name)}`" data-agent-row :data-session="session.id" @click="$emit('open', session)">
    <span class="srow__top">
      <LiveDot :state="state" />
      <span class="srow__who"
        ><span class="srow__role">{{ role }}</span
        ><span aria-hidden="true"> · </span><span class="srow__name">{{ visibleText(session.name) }}</span></span
      >
      <span v-if="word" class="srow__word" aria-hidden="true">{{ word }}</span>
      <RelativeTime class="srow__since" :at="session.started_at" />
    </span>
    <span v-if="!compact && ticket" class="srow__ticket"
      ><span class="srow__number">#{{ ticket.n }}</span> {{ visibleText(ticket.title) }}</span
    >
    <span v-if="!compact && meta" class="srow__meta">{{ meta }}</span>
    <span v-if="kept" class="srow__meta" data-kept>{{ kept }}</span>
  </button>
</template>

<style scoped>
.srow {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  width: 100%;
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
  background: transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: left;
  cursor: pointer;
  transition:
    background-color var(--omo-duration) ease,
    border-color var(--omo-duration) ease;
}
.srow:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
@media (hover: hover) {
  .srow:hover {
    border-left-color: var(--omo-accent);
    background: var(--omo-accent-soft);
  }
}
.srow__top {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.srow__who,
.srow__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.srow__role {
  color: var(--omo-muted);
}
.srow__word {
  flex-shrink: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.srow__since {
  flex-shrink: 0;
  margin-left: auto;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.srow__ticket,
.srow__meta {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.srow__number {
  color: var(--omo-muted);
}
.srow__meta {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
