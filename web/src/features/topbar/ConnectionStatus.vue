<script setup lang="ts">
// Whether the board is live. Quiet while it is; while the connection to omo
// is lost or the board is catching up it says so, with the time until the
// next attempt and a compact "Retry now" button, so the top bar keeps its
// layout. Changes are announced politely once the
// board has been live, never the countdown.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import IconButton from '../../components/ui/IconButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useBoard } from '../../store'

type Kind = 'live' | 'catching-up' | 'connecting' | 'reconnecting' | 'retrying' | 'offline'

const store = useBoard()
const announcer = useAnnouncer()

/** Whether the stream has been live at least once since this component appeared. */
const wasLive = ref(false)

const kind = computed<Kind>(() => {
  switch (store.connection.value) {
    case 'live':
      return store.syncing.value ? 'catching-up' : 'live'
    case 'retrying':
      return 'retrying'
    case 'connecting':
      return wasLive.value ? 'reconnecting' : 'connecting'
    default:
      return 'offline'
  }
})

// Countdown to the next attempt.
const deadline = ref<number | null>(null)
const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | undefined

function stopTicker() {
  if (ticker !== undefined) clearInterval(ticker)
  ticker = undefined
}

watch(
  () => store.retryInMs.value,
  (ms) => {
    stopTicker()
    if (ms === null) {
      deadline.value = null
      return
    }
    now.value = Date.now()
    deadline.value = now.value + ms
    ticker = setInterval(() => {
      now.value = Date.now()
    }, 250)
  },
  { immediate: true },
)
onBeforeUnmount(stopTicker)

const seconds = computed(() => (deadline.value === null ? null : Math.max(1, Math.ceil((deadline.value - now.value) / 1000))))

const text = computed(() => {
  switch (kind.value) {
    case 'live':
      return 'live'
    case 'catching-up':
      return 'catching up…'
    case 'connecting':
      return 'connecting…'
    case 'reconnecting':
      return 'reconnecting…'
    case 'retrying':
      return seconds.value === null ? 'reconnecting…' : `reconnecting in ${seconds.value} s`
    default:
      return 'offline'
  }
})

const hint = computed(() =>
  kind.value === 'live' ? 'The board follows omo live.' : 'The board may be out of date until it is live again.',
)

/** What is announced: only the broad state, so a flapping countdown says nothing. */
const phase = computed(() => {
  if (kind.value === 'live' || kind.value === 'catching-up') return 'live'
  if (kind.value === 'offline') return 'offline'
  return 'lost'
})
const SAY = { live: 'The board is live again.', lost: 'Connection to omo lost. Reconnecting.', offline: 'The board is offline.' }

watch(phase, (p, old) => {
  if (p === 'live' && !wasLive.value) {
    wasLive.value = true
    return
  }
  if (wasLive.value && p !== old) announcer.polite(SAY[p])
}, { immediate: true })
</script>

<template>
  <div class="conn" :class="`conn--${kind}`" data-connection :data-state="kind" :title="hint">
    <span class="conn__dot" aria-hidden="true" />
    <span class="conn__text">{{ text }}</span>
    <IconButton v-if="kind === 'retrying'" label="Retry now" size="sm" class="conn__retry" @click="store.reconnectNow()">
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
        <path d="M10.2 6.6A4.2 4.2 0 1 1 9 2.9" />
        <path d="M9.6 .8v2.6H7" />
      </svg>
    </IconButton>
  </div>
</template>

<style scoped>
.conn {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 calc(var(--omo-space) * 1.5);
  min-width: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.conn__dot {
  flex: none;
  width: calc(var(--omo-space) * 2);
  height: calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-muted);
}
.conn--live .conn__dot {
  border-color: var(--omo-ok);
  background: var(--omo-ok);
}
.conn--catching-up .conn__dot,
.conn--connecting .conn__dot {
  border-color: var(--omo-muted);
}
.conn--reconnecting,
.conn--retrying {
  color: var(--omo-warn);
}
.conn--reconnecting .conn__dot,
.conn--retrying .conn__dot {
  border-color: var(--omo-warn);
}
.conn--offline {
  color: var(--omo-danger);
}
.conn--offline .conn__dot {
  border-color: var(--omo-danger);
}

/* Phones: while live only the dot shows; the word stays for assistive technology. */
@container topbar (max-width: 767.98px) {
  .conn--live .conn__text {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
}
</style>
