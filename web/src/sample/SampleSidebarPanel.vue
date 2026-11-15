<script setup lang="ts">
// The content of one sidebar tab, with static data: the running agents, the
// CEO's terminal, or the user's own shells. With `usage` the Agents tab
// starts with the usage of each account.
import IconButton from '../components/ui/IconButton.vue'
import LiveDot from '../components/ui/LiveDot.vue'
import RelativeTime from '../components/ui/RelativeTime.vue'
import UiButton from '../components/ui/UiButton.vue'
import { visibleText } from '../components/ui/visibleText'
import { ROLE_LABELS } from '../features/board/model'
import { minutesAgo, sampleAgents, sampleShells } from './sampleBoard'
import SampleTerminal from './SampleTerminal.vue'
import SampleUsagePanel from './SampleUsagePanel.vue'

defineProps<{ tab: string; usage?: boolean }>()

const STATE_WORDS = { running: 'running', starting: 'starting', waiting: 'parked' } as const
type LiveState = keyof typeof STATE_WORDS
const live = (s: string): s is LiveState => s in STATE_WORDS

const agents = sampleAgents.filter((a) => live(a.ref.state))
const inUse = Math.min(4, agents.filter((a) => a.ref.state !== 'waiting').length)
const ceoSince = minutesAgo(48)
</script>

<template>
  <div class="spanel">
    <template v-if="tab === 'agents'">
      <SampleUsagePanel v-if="usage" />
      <div class="spanel__head">
        <p class="spanel__slots">Slots: <span class="spanel__count">{{ inUse }} of 4</span> in use</p>
        <p class="spanel__note">Agents waiting in <code>omo ai wait</code> hold no slot.</p>
      </div>
      <ul class="spanel__list" aria-label="Running agents">
        <li v-for="a in agents" :key="a.ref.session">
          <button type="button" class="srow" :title="`Open the terminal of ${a.ref.name}`">
            <span class="srow__top">
              <LiveDot v-if="live(a.ref.state)" :state="a.ref.state" />
              <span class="srow__who"
                ><span class="srow__role">{{ ROLE_LABELS[a.ref.role] }}</span
                ><span aria-hidden="true"> · </span><span class="srow__name">{{ a.ref.name }}</span></span
              >
              <span v-if="a.ref.state !== 'running' && live(a.ref.state)" class="srow__word" aria-hidden="true">{{
                STATE_WORDS[a.ref.state]
              }}</span>
              <RelativeTime class="srow__since" :at="a.since" />
            </span>
            <span class="srow__ticket"><span class="srow__number">#{{ a.ticket }}</span> {{ a.title }}</span>
            <span class="srow__meta">{{ a.project }} · {{ a.model }}</span>
          </button>
        </li>
      </ul>
    </template>

    <template v-else-if="tab === 'ceo'">
      <div class="spanel__bar">
        <p class="spanel__who">
          <LiveDot state="running" /> <span>CEO · sharp-falcon</span>
          <span class="spanel__muted">since <RelativeTime :at="ceoSince" /></span>
        </p>
        <UiButton size="sm">New session</UiButton>
      </div>
      <SampleTerminal label="Terminal of the CEO" title="claude · CEO" screen="agent" note="Typing here types into the CEO." />
    </template>

    <template v-else>
      <div class="spanel__bar">
        <p class="spanel__slots">Your terminals keep running when the browser closes.</p>
        <UiButton size="sm">New terminal…</UiButton>
      </div>
      <ul class="spanel__list" aria-label="Your terminals">
        <li v-for="(s, i) in sampleShells" :key="s.id" class="srow srow--shell" :aria-current="i === 0 ? 'true' : undefined">
          <span class="srow__top">
            <span class="srow__name">{{ visibleText(s.title) }}</span>
            <span class="srow__word" :class="{ 'srow__word--off': s.state === 'exited' }">{{ s.state }}</span>
            <RelativeTime class="srow__since" :at="s.since" />
            <IconButton :label="`Close terminal ${s.title} in ${s.where}`" size="sm">
              <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
              </svg>
            </IconButton>
          </span>
          <span class="srow__meta">{{ visibleText(s.where) }}</span>
          <span v-if="s.ticket !== null" class="srow__keeps">Keeps the worktree of #{{ s.ticket }} until it is closed.</span>
        </li>
      </ul>
      <SampleTerminal label="Terminal zsh in the worktree of #13" title="zsh · ~/.local/omo/worktrees/omo/13" />
    </template>
  </div>
</template>

<style scoped>
.spanel {
  display: grid;
  align-content: start;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 3);
}
.spanel__head {
  display: grid;
  gap: var(--omo-space);
}
.spanel__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.spanel__slots,
.spanel__who {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 calc(var(--omo-space) * 1.5);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.spanel__slots {
  display: block;
}
.spanel__count {
  color: var(--omo-accent);
}
.spanel__note,
.spanel__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.spanel__note code {
  color: var(--omo-fg);
}
.spanel__list {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
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
  transition:
    background-color var(--omo-duration) ease,
    border-color var(--omo-duration) ease;
}
button.srow {
  cursor: pointer;
}
button.srow:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.srow[aria-current='true'] {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
@media (hover: hover) {
  button.srow:hover {
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
.srow__who {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.srow__role {
  color: var(--omo-muted);
}
.srow__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.srow__word {
  flex-shrink: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.srow--shell .srow__word {
  color: var(--omo-ok);
}
.srow--shell .srow__word--off {
  color: var(--omo-muted);
}
.srow__since {
  flex-shrink: 0;
  margin-left: auto;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.srow__ticket,
.srow__meta,
.srow__keeps {
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
.srow__keeps {
  color: var(--omo-warn);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: normal;
}
</style>
