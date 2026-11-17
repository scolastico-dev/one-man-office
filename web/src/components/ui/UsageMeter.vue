<script lang="ts">
import { timeKey } from '../../api/time'

export type UsageMeterState = 'ok' | 'soft' | 'hard' | 'unknown' | 'unmetered'

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
/** Below this the words name the minutes beside the hours ("1 h 59 min", not "1 h"). */
const MINUTES_SHOWN_BELOW = 10 * HOUR
/** Further away than this shows the date instead of a count of days. */
const DAYS_SHOWN = 7

/** Milliseconds since the epoch, or null for text that is no RFC 3339 time. */
function msOf(at: string): number | null {
  const key = timeKey(at)
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$/.test(key)) return null
  const t = Date.parse(`${key.slice(0, 23)}Z`)
  return Number.isNaN(t) ? null : t
}

/**
 * When a usage window starts again, in words: "resets in 1 h 59 min" (minutes
 * beside the hours below ten hours), "resets in 14 h", "resets on
 * 1 Dec 2026", "resets now" once the time has passed (the next reading
 * brings the new window). Empty for text that is no RFC 3339 time.
 */
export function resetWords(at: string, now: number): string {
  const t = msOf(at)
  if (t === null) return ''
  const ahead = t - now
  if (ahead <= 0) return 'resets now'
  if (ahead < MINUTE) return 'resets in under a minute'
  if (ahead < HOUR) return `resets in ${Math.floor(ahead / MINUTE)} min`
  if (ahead < MINUTES_SHOWN_BELOW) {
    const hours = Math.floor(ahead / HOUR)
    const minutes = Math.floor((ahead % HOUR) / MINUTE)
    return minutes === 0 ? `resets in ${hours} h` : `resets in ${hours} h ${minutes} min`
  }
  if (ahead < DAY) return `resets in ${Math.floor(ahead / HOUR)} h`
  const days = Math.floor(ahead / DAY)
  if (days <= DAYS_SHOWN) return days === 1 ? 'resets in 1 day' : `resets in ${days} days`
  return `resets on ${new Date(t).toLocaleDateString(undefined, { dateStyle: 'medium' })}`
}
</script>

<script setup lang="ts">
// How full one usage window (or a model) is: the share used as a number, a
// bar with marks where agents start to wait (soft) and where they are
// stopped (hard), the state in words beside the number, and when the window
// resets. Colour only repeats what the words say.
import { computed, useId } from 'vue'
import { useNow } from './useNow'
import { visibleText } from './visibleText'

const props = withDefaults(
  defineProps<{
    /** The window's or the model's name, or a window's label; also the meter's accessible name. */
    label: string
    /** The technical name of a window shown by its label: the label's tooltip. Left out, or equal to the label: none. */
    name?: string
    /** Share used, 0 to 100 (more is shown as 100); `null` while unknown or unmetered. */
    percent: number | null
    /** Where new agents stop starting on the model, in percent. */
    soft?: number
    /** Where running agents are stopped, in percent. */
    hard?: number
    /** The server's verdict; left out, it follows `percent` and the thresholds. */
    state?: UsageMeterState
    /** RFC 3339, as the server writes it; `null` or left out when the window does not say. */
    resetsAt?: string | null
  }>(),
  { name: undefined, soft: undefined, hard: undefined, state: undefined, resetsAt: null },
)

const STATE_WORDS: Record<UsageMeterState, string> = {
  ok: '',
  soft: 'near its limit',
  hard: 'at its limit',
  unknown: 'unknown',
  unmetered: 'not metered',
}

const labelId = useId()

const now = useNow()

/** The share used, between 0 and 100, or null when there is none. */
const value = computed(() => {
  const p = props.percent
  if (p === null || !Number.isFinite(p)) return null
  return Math.min(100, Math.max(0, p))
})

const shownState = computed<UsageMeterState>(() => {
  if (props.state !== undefined) return props.state
  const v = value.value
  if (v === null) return 'unknown'
  if (props.hard !== undefined && v >= props.hard) return 'hard'
  if (props.soft !== undefined && v >= props.soft) return 'soft'
  return 'ok'
})

// Whole numbers, cut down, so a window just under a threshold never reads as on it.
const valueText = computed(() => (value.value === null ? '?' : `${Math.floor(value.value)} %`))
const stateWords = computed(() => STATE_WORDS[shownState.value])

const resetAt = computed(() => (props.resetsAt ? props.resetsAt : null))
const reset = computed(() => (resetAt.value === null ? '' : resetWords(resetAt.value, now.value)))
const resetTitle = computed(() => {
  const t = resetAt.value === null ? null : msOf(resetAt.value)
  return t === null ? undefined : new Date(t).toLocaleString(undefined, { dateStyle: 'full', timeStyle: 'medium' })
})

const percentText = (n: number) => `${n} %`
const limits = computed(() => {
  const parts: string[] = []
  if (props.soft !== undefined) parts.push(`waits at ${percentText(props.soft)}`)
  if (props.hard !== undefined) parts.push(`stops at ${percentText(props.hard)}`)
  return parts.join(', ')
})

const valueTextForReaders = computed(() => {
  const v = value.value
  if (v === null || shownState.value === 'unmetered') return [stateWords.value || 'unknown', reset.value].filter(Boolean).join(', ')
  return [`${Math.floor(v)} % used`, stateWords.value, reset.value].filter(Boolean).join(', ')
})

const markAt = (n: number) => `${Math.min(100, Math.max(0, n))}%`
</script>

<template>
  <div class="umeter" :class="`umeter--${shownState}`" :data-state="shownState">
    <div class="umeter__head">
      <span :id="labelId" class="umeter__label" :title="name !== undefined && name !== label ? visibleText(name) : undefined">{{ visibleText(label) }}</span>
      <span class="umeter__value" data-usage-value>{{ valueText }}</span>
      <span v-if="stateWords" class="umeter__state" data-usage-state>{{ stateWords }}</span>
    </div>
    <!-- A meter must carry a value: without one it is 0, and aria-valuetext says "unknown" or "not metered". -->
    <div
      role="meter"
      class="umeter__bar"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="value === null || shownState === 'unmetered' ? 0 : value"
      :aria-valuetext="valueTextForReaders"
      :aria-labelledby="labelId"
    >
      <span v-if="value !== null && shownState !== 'unmetered'" class="umeter__fill" data-usage-fill :style="{ width: `${value}%` }" />
      <span v-if="soft !== undefined" class="umeter__mark umeter__mark--soft" data-mark="soft" :style="{ left: markAt(soft) }" />
      <span v-if="hard !== undefined" class="umeter__mark umeter__mark--hard" data-mark="hard" :style="{ left: markAt(hard) }" />
    </div>
    <p v-if="reset || limits" class="umeter__meta">
      <span v-if="reset" data-usage-reset
        ><time :datetime="resetAt ?? undefined" :title="resetTitle">{{ reset }}</time></span
      >
      <span v-if="limits" class="umeter__limits" data-usage-limits>{{ limits }}</span>
    </p>
  </div>
</template>

<style scoped>
.umeter {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  --umeter-tone: var(--omo-accent);
}
.umeter--soft {
  --umeter-tone: var(--omo-warn);
}
.umeter--hard {
  --umeter-tone: var(--omo-danger);
}
.umeter__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 calc(var(--omo-space) * 2);
  min-width: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.umeter__label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.umeter__value {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.umeter--soft .umeter__value,
.umeter--hard .umeter__value {
  color: var(--umeter-tone);
}
.umeter--unknown .umeter__value,
.umeter--unmetered .umeter__value {
  color: var(--omo-muted);
}
.umeter__state {
  flex-shrink: 0;
  color: var(--umeter-tone);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.umeter--hard .umeter__state {
  font-weight: 700;
}
.umeter--unknown .umeter__state,
.umeter--unmetered .umeter__state {
  color: var(--omo-muted);
}
/* The track: a frame on the page's rule colour; the marks reach past it, so they show over any fill. */
.umeter__bar {
  position: relative;
  height: calc(var(--omo-space) * 2);
  margin: var(--omo-space) 0;
  border: var(--omo-border) solid var(--omo-line-strong);
}
.umeter__fill {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  background: var(--umeter-tone);
}
.umeter__mark {
  position: absolute;
  top: calc(var(--omo-space) * -1 - var(--omo-border));
  bottom: calc(var(--omo-space) * -1 - var(--omo-border));
  width: var(--omo-border);
  background: var(--omo-fg);
  transform: translateX(-50%);
}
.umeter__mark--hard {
  width: calc(var(--omo-border) * 2);
}
.umeter__meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 0 calc(var(--omo-space) * 3);
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.umeter__limits {
  margin-left: auto;
}
</style>
