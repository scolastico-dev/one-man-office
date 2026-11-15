<script setup lang="ts">
// Picks the screen: the board, the locked screen, the page for a board that
// could not be loaded, or (briefly, and only after a moment) a loading line.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import LiveAnnouncer from './components/ui/LiveAnnouncer.vue'
import Toaster from './components/ui/Toaster.vue'
import LockedScreen from './features/auth/LockedScreen.vue'
import UnreachableScreen from './features/auth/UnreachableScreen.vue'
import LiveBoard from './features/board/LiveBoard.vue'
import { useSession } from './session'
import { provideBoard } from './store'

/** How long the loading line waits, so a quick start shows no flash of it. */
const LOADING_DELAY_MS = 400

const session = useSession()
provideBoard(session.store)
const { phase, lockReason, problem } = session

const route = useRoute()
const sample = computed(() => route.name === 'sample')
watch(
  sample,
  (s) => {
    if (!s) session.start()
  },
  { immediate: true },
)

const slow = ref(false)
let slowTimer: ReturnType<typeof setTimeout> | undefined
watch(
  phase,
  (p) => {
    clearTimeout(slowTimer)
    slow.value = false
    if (p === 'loading' || p === 'idle') slowTimer = setTimeout(() => (slow.value = true), LOADING_DELAY_MS)
  },
  { immediate: true },
)

// Coming back online is a good moment to try again.
function onOnline() {
  session.retry()
}
onMounted(() => window.addEventListener('online', onOnline))
onBeforeUnmount(() => {
  window.removeEventListener('online', onOnline)
  clearTimeout(slowTimer)
})
</script>

<template>
  <RouterView v-if="sample" />
  <LiveBoard v-else-if="phase === 'ready'" />
  <LockedScreen v-else-if="phase === 'locked'" :reason="lockReason" @unlocked="session.unlock" />
  <UnreachableScreen v-else-if="phase === 'unreachable'" :problem="problem" @retry="session.retry()" />
  <main v-else class="loading" aria-busy="true" data-loading>
    <p v-if="slow" class="loading__text">Loading the board…</p>
  </main>
  <Toaster />
  <LiveAnnouncer />
</template>

<style scoped>
.loading {
  display: grid;
  place-items: center;
  min-height: 100dvh;
}
.loading__text {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
