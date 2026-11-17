<script setup lang="ts">
// The usage block of the Agents tab with static data: each account's windows
// as meters, a script that failed, and the models agents are kept off.
import { useId } from 'vue'
import RelativeTime from '../components/ui/RelativeTime.vue'
import UiButton from '../components/ui/UiButton.vue'
import UsageMeter, { resetWords } from '../components/ui/UsageMeter.vue'
import { visibleText } from '../components/ui/visibleText'
import { modelWindowWords } from '../features/usage/windowLabel'
import { sampleMeter, sampleModelUsage, sampleReadings } from './sampleExtensions'

const headingId = useId()
const now = Date.now()

const STATE_WORDS = { soft: 'near its limit', hard: 'at its limit' } as const
const limited = sampleModelUsage.filter((m): m is typeof m & { state: 'soft' | 'hard' } => m.state === 'soft' || m.state === 'hard')
const settingsHref = '?settings=usage'
</script>

<template>
  <section class="susage" :aria-labelledby="headingId">
    <div class="susage__bar">
      <h2 :id="headingId" class="susage__title">Usage</h2>
      <a class="susage__link" :href="settingsHref">Usage settings</a>
      <UiButton size="sm">Refresh</UiButton>
    </div>

    <ul class="susage__accounts">
      <li v-for="r in sampleReadings" :key="r.key" class="susage__account">
        <p class="susage__who">
          <span class="susage__profiles">{{ r.profiles.map(visibleText).join(', ') }}</span>
          <span class="susage__muted">
            {{ visibleText(r.script) }} · <template v-if="r.fetched_at">updated <RelativeTime :at="r.fetched_at" /></template>
          </span>
        </p>
        <p v-if="r.error" class="susage__error">
          <span class="susage__error-word">Script failed:</span> {{ visibleText(r.error) }}. Its models count as available.
        </p>
        <ul v-if="r.windows.length" class="susage__meters" :aria-label="`Windows of ${r.profiles.join(', ')}`">
          <li v-for="m in r.windows.map((w) => sampleMeter(w, r))" :key="m.name">
            <UsageMeter :label="m.title" :name="m.name" :percent="m.percent" :state="m.state" :soft="m.soft" :hard="m.hard" :resets-at="m.resetsAt" />
          </li>
        </ul>
      </li>
    </ul>

    <div v-if="limited.length" class="susage__limited">
      <p class="susage__muted">Agents are kept off these models:</p>
      <ul class="susage__models">
        <li v-for="m in limited" :key="m.model" :class="`susage__model--${m.state}`">
          <span class="susage__model">{{ visibleText(m.model) }}</span> —
          <span class="susage__state">{{ STATE_WORDS[m.state] }}</span
          ><template v-if="m.resets_at">, {{ resetWords(m.resets_at, now) }}</template
          ><template v-if="modelWindowWords(m)"> ({{ modelWindowWords(m) }})</template>
        </li>
      </ul>
    </div>
  </section>
</template>

<style scoped>
.susage {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  padding-bottom: calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.susage__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
}
.susage__title {
  margin: 0 auto 0 0;
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.susage__link {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@media (hover: hover) {
  .susage__link:hover {
    color: var(--omo-accent);
  }
}
.susage__accounts {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  margin: 0;
  padding: 0;
  list-style: none;
}
.susage__account {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.susage__who {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 0 calc(var(--omo-space) * 3);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.susage__profiles {
  min-width: 0;
  overflow-wrap: anywhere;
}
.susage__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.susage__error {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2.5);
  border-left: var(--omo-edge) solid var(--omo-danger);
  background: var(--omo-danger-soft);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.susage__error-word {
  color: var(--omo-danger);
  font-weight: 700;
}
.susage__meters {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0 0 0 calc(var(--omo-space) * 2.5);
  border-left: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.susage__limited {
  display: grid;
  gap: var(--omo-space);
}
.susage__models {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.susage__model {
  color: var(--omo-fg);
}
.susage__model--soft .susage__state {
  color: var(--omo-warn);
}
.susage__model--hard .susage__state {
  color: var(--omo-danger);
  font-weight: 700;
}
</style>
