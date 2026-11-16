<script setup lang="ts">
// The usage block of the Agents tab: per account its profiles and when it was
// last read, a meter for each window (by its label where the script gives
// one, the name as its tooltip) with the thresholds that apply, the
// script's error when its last run failed, and the models agents are kept
// off. Names, window names and error texts come from outside and are shown
// as text.
import { computed, onBeforeUnmount, ref, useId } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import type { ModelUsage, UsageReading, UsageWindow } from '../../api/types'
import RelativeTime from '../../components/ui/RelativeTime.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UsageMeter, { resetWords } from '../../components/ui/UsageMeter.vue'
import { visibleText } from '../../components/ui/visibleText'
import { settingsLocation } from '../../locations'
import { useBoard } from '../../store'
import { judgeWindow, staleWords } from '../usage/usageVerdict'
import { modelWindowWords, windowTitle } from '../usage/windowLabel'
import { useUsage } from './useUsage'

const usage = useUsage()
const store = useBoard()
const route = useRoute()
const headingId = useId()

const MINUTE = 60_000
const now = ref(Date.now())
const timer = setInterval(() => (now.value = Date.now()), MINUTE)
onBeforeUnmount(() => clearInterval(timer))

const STATE_WORDS = { soft: 'near its limit', hard: 'at its limit' } as const

const readings = computed(() => usage.state.value?.readings ?? [])
const notes = computed(() => (usage.state.value?.models ?? []).filter((m) => m.note !== ''))
const limited = computed(() => (usage.state.value?.models ?? []).filter((m): m is ModelUsage & { state: 'soft' | 'hard' } => m.state === 'soft' || m.state === 'hard'))

/** A window judged as the server judges the models it counts for: the thresholds that apply, unknown after an old reading or 30 minutes after a failed run, 0 % past its reset. */
const meters = (r: UsageReading) =>
  r.windows.map((w: UsageWindow) => ({ name: w.name, title: windowTitle(w), ...judgeWindow(w, r, store.state.general?.usage, now.value) }))

const names = (r: UsageReading) => r.profiles.map(visibleText).join(', ')
/** The server's cleaned error as one sentence, without its own full stop. */
const errorText = (r: UsageReading) => visibleText(r.error).replace(/\.+$/, '')
/** The words on old readings that still count after a failed run; empty otherwise. */
const stale = (r: UsageReading) => staleWords({ ...r, error: visibleText(r.error) }, now.value)
</script>

<template>
  <section class="usage" :aria-labelledby="headingId" data-usage>
    <div class="usage__bar">
      <h2 :id="headingId" class="usage__title">Usage</h2>
      <RouterLink class="usage__link" :to="settingsLocation('usage', route)" data-usage-settings>Usage settings</RouterLink>
      <UiButton v-if="readings.length > 0 || usage.error.value" size="sm" :disabled="usage.refreshing.value" data-usage-refresh @click="usage.refresh()">
        {{ usage.refreshing.value ? 'Refreshing…' : 'Refresh' }}
      </UiButton>
    </div>

    <p v-if="usage.error.value" class="usage__failure" role="alert" data-usage-error>{{ visibleText(usage.error.value) }}</p>

    <template v-if="usage.loaded.value">
      <p v-if="readings.length === 0" class="usage__muted" data-usage-none>No profile reports usage.</p>

      <ul v-else class="usage__accounts" aria-label="Accounts">
        <li v-for="r in readings" :key="r.key" class="usage__account" :data-usage-account="r.key">
          <p class="usage__who">
            <span class="usage__profiles">{{ names(r) }}</span>
            <span class="usage__muted">
              {{ visibleText(r.script) }} ·
              <template v-if="r.running">reading…</template>
              <template v-else-if="r.fetched_at">updated <RelativeTime :at="r.fetched_at" /></template>
              <template v-else>not read yet</template>
            </span>
          </p>
          <p v-if="stale(r)" class="usage__stale" data-usage-stale><span class="usage__stale-word">Old readings:</span> {{ stale(r) }}</p>
          <p v-else-if="r.error" class="usage__error" data-usage-script-error>
            <span class="usage__error-word">Script failed:</span> {{ errorText(r) }}. Its models count as available.
          </p>
          <ul v-if="r.windows.length > 0" class="usage__meters" :aria-label="`Windows of ${names(r)}`">
            <li v-for="m in meters(r)" :key="m.name" :data-usage-window="m.name">
              <UsageMeter :label="m.title" :name="m.name" :percent="m.percent" :state="m.state" :soft="m.soft" :hard="m.hard" :resets-at="m.resetsAt" />
            </li>
          </ul>
        </li>
      </ul>

      <div v-if="limited.length > 0" class="usage__limited" data-usage-limited>
        <p class="usage__muted">Agents are kept off these models:</p>
        <ul class="usage__models">
          <li v-for="m in limited" :key="m.model" :class="`usage__model--${m.state}`" :data-usage-model="m.model" :data-usage-window-name="m.window || undefined">
            <span class="usage__model">{{ visibleText(m.model) }}</span> —
            <span class="usage__state">{{ STATE_WORDS[m.state] }}</span
            ><template v-if="m.resets_at && resetWords(m.resets_at, now)">, {{ resetWords(m.resets_at, now) }}</template
            ><template v-if="modelWindowWords(m)"> ({{ modelWindowWords(m) }})</template>
          </li>
        </ul>
      </div>

      <ul v-if="notes.length > 0" class="usage__notes" aria-label="What the readings lack">
        <li v-for="m in notes" :key="m.model" class="usage__muted" :data-usage-note="m.model">{{ visibleText(m.note).replace(/\.+$/, '') }}.</li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.usage {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  padding-bottom: calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.usage__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
}
.usage__title {
  margin: 0 auto 0 0;
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.usage__link {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@media (hover: hover) {
  .usage__link:hover {
    color: var(--omo-accent);
  }
}
.usage__accounts {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  margin: 0;
  padding: 0;
  list-style: none;
}
.usage__account {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.usage__who {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 0 calc(var(--omo-space) * 3);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.usage__profiles {
  min-width: 0;
  overflow-wrap: anywhere;
}
.usage__muted {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.usage__error,
.usage__failure {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2.5);
  border-left: var(--omo-edge) solid var(--omo-danger);
  background: var(--omo-danger-soft);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.usage__stale {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2.5);
  border-left: var(--omo-edge) solid var(--omo-warn);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.usage__stale-word {
  color: var(--omo-warn);
  font-weight: 700;
}
.usage__notes {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  margin: 0;
  padding: 0;
  list-style: none;
  overflow-wrap: anywhere;
}
.usage__error-word {
  color: var(--omo-danger);
  font-weight: 700;
}
.usage__meters {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0 0 0 calc(var(--omo-space) * 2.5);
  border-left: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.usage__limited {
  display: grid;
  gap: var(--omo-space);
}
.usage__models {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.usage__model {
  color: var(--omo-fg);
}
.usage__model--soft .usage__state {
  color: var(--omo-warn);
}
.usage__model--hard .usage__state {
  color: var(--omo-danger);
  font-weight: 700;
}
</style>
