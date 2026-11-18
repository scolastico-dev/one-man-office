<script setup lang="ts">
// Picks the screen: the board, the locked screen, the page for a board that
// could not be loaded, or (briefly, and only after a moment) a loading line.
// Once the board was shown, it stays mounted under the other screens, hidden
// and inert, so a lock (a refused token, also in answer to a save) loses no
// draft: after the unlock it is shown again as it was. A terminal's own
// window (`/terminal/…`) and the OAuth consent page (`/oauth/authorize`) are
// kept the same way in place of the board.
import { computed, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { lazyComponent } from './components/ui/chunkFailure'
import { BOARD_SUSPENDED } from './components/ui/dialogHost'
import LiveAnnouncer from './components/ui/LiveAnnouncer.vue'
import Toaster from './components/ui/Toaster.vue'
import LockedScreen from './features/auth/LockedScreen.vue'
import UnreachableScreen from './features/auth/UnreachableScreen.vue'
import LiveBoard from './features/board/LiveBoard.vue'
import PluginHost from './features/plugins/PluginHost.vue'
import { useSession } from './session'
import { provideBoard } from './store'

/** One terminal alone in its own window; fetched only there. */
const TerminalWindow = lazyComponent(() => import('./features/terminal/TerminalWindow.vue'))
/** The OAuth consent page of the MCP endpoint; fetched only there. */
const OAuthConsent = lazyComponent(() => import('./features/oauth/OAuthConsent.vue'))

/** How long the loading line waits, so a quick start shows no flash of it. */
const LOADING_DELAY_MS = 400

const session = useSession()
provideBoard(session.store)
const { phase, lockReason, problem } = session

const route = useRoute()
const sample = computed(() => route.name === 'sample')
const terminalWindow = computed(() => route.name === 'terminal')
const consentPage = computed(() => route.name === 'oauth-consent')
watch(
  sample,
  (s) => {
    if (!s) session.start()
  },
  { immediate: true },
)

/** The board was shown once: from then on it is kept mounted, hidden while another screen shows. */
const kept = ref(false)
watch(
  phase,
  (p) => {
    if (p === 'ready') kept.value = true
  },
  { immediate: true },
)
const boardShown = computed(() => phase.value === 'ready')
provide(
  BOARD_SUSPENDED,
  computed(() => kept.value && !boardShown.value),
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
  <template v-else>
    <div
      v-if="kept"
      v-show="boardShown"
      class="app-board"
      :inert="!boardShown || undefined"
      :aria-hidden="boardShown ? undefined : 'true'"
      data-board-kept
    >
      <PluginHost v-if="!consentPage" />
      <TerminalWindow v-if="terminalWindow" />
      <OAuthConsent v-else-if="consentPage" />
      <LiveBoard v-else />
    </div>
    <template v-if="!boardShown">
      <LockedScreen v-if="phase === 'locked'" :reason="lockReason" @unlocked="session.unlock" />
      <UnreachableScreen v-else-if="phase === 'unreachable'" :problem="problem" @retry="session.retry()" />
      <main v-else class="loading" aria-busy="true" data-loading>
        <p v-if="slow" class="loading__text">Loading the board…</p>
      </main>
    </template>
  </template>
  <Toaster />
  <LiveAnnouncer />
</template>

<style scoped>
/* Only a hook for hiding the board: it takes no room of its own. */
.app-board {
  display: contents;
}
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
