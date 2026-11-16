<script setup lang="ts">
// A ticket on a page of its own, at /ticket/<n>, in place of the board's
// columns (top bar and sidebar stay, as for the settings). It shows the same
// parts as the panel, laid out for the space: the head across the top (type,
// number, title, state, actions), then the tabs at the top of the main
// column with the selected tab filling the height left, and the fields in a
// details column on the right. The description is the first tab, Overview,
// so every tab starts at the top and Comments, Diff and the terminal get the
// whole height; on a page too narrow for the details column (or with it
// hidden) the fields join the description in Overview. Below 768 px
// everything stacks and the page scrolls as one.
//
// Ways out: the top bar's "Back to board" (or `b`) with the board's filters,
// "Open in panel" (`p`) for the quick view over the board. Escape never leaves the page; it
// only closes what is open on it.
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { BOARD_SUSPENDED } from '../../../components/ui/dialogHost'
import { boardLocation, ticketLocation } from '../../../locations'
import TicketActions from '../TicketActions.vue'
import TicketDescription from '../TicketDescription.vue'
import TicketFields from '../TicketFields.vue'
import TicketHeader from '../TicketHeader.vue'
import TicketLeaveAsk from '../TicketLeaveAsk.vue'
import TicketLoadNotice from '../TicketLoadNotice.vue'
import { provideTicketLayout } from '../ticketLayout'
import TicketTabs from '../tabs/TicketTabs.vue'
import { pageTabForPanel } from '../tabs/ticketTabs'
import { useTicketView } from '../useTicketView'
import { BACK_TO_BOARD_KEY, plainKey, SWITCH_VIEW_KEY } from '../viewKeys'
import { usePagePrefs } from './pagePrefs'

/** The page is wide enough for the details column from this width on (px). */
const DETAILS_FROM = 960

const route = useRoute()
const router = useRouter()
const number = Number(route.params.n)
const titleId = useId()
provideTicketLayout('page')

const root = ref<HTMLElement>()
const header = ref<InstanceType<typeof TicketHeader>>()
const ask = ref<InstanceType<typeof TicketLeaveAsk>>()

const view = useTicketView(number, {
  root,
  discardTitle: () => header.value?.discard(),
  focusQuestion: () => ask.value?.focus(),
})
const { ticket, confirmLeave } = view
const prefs = usePagePrefs()

const boardTarget = computed(() => boardLocation(route))
const panelTarget = computed(() => ticketLocation(number, route, { tab: pageTabForPanel(route.query.tab) }))

// Room for the details column: measured on the page itself (the sidebar takes its share of the window).
const wide = ref(false)
let observer: ResizeObserver | null = null
const detailsBeside = computed(() => wide.value && prefs.details)

/** The heading the focus goes to on arrival: the title, or the placeholder while the ticket loads. */
function focusHeading() {
  root.value?.querySelector<HTMLElement>('[data-ticket-title], [data-page-heading]')?.focus({ preventScroll: true })
}

onMounted(() => {
  const el = root.value
  if (el && typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width ?? el.clientWidth
      wide.value = w >= DETAILS_FROM
    })
    observer.observe(el)
  } else if (el) wide.value = el.clientWidth >= DETAILS_FROM
  void nextTick(focusHeading)
})

// The placeholder heading gives way to the title once the ticket is read: the focus follows.
watch(
  () => ticket.value !== null,
  (has, had) => {
    if (has && !had) {
      const active = document.activeElement
      if (!active || active === document.body || active.matches('[data-page-heading]') || !active.isConnected) void nextTick(focusHeading)
    }
  },
)

const suspended = inject(BOARD_SUSPENDED, null)

function onWindowKeydown(e: KeyboardEvent) {
  if (suspended?.value || confirmLeave.value || document.querySelector('dialog[open]')) return
  if (plainKey(e, SWITCH_VIEW_KEY)) {
    e.preventDefault()
    void router.push(panelTarget.value)
  } else if (plainKey(e, BACK_TO_BOARD_KEY)) {
    e.preventDefault()
    void router.push(boardTarget.value)
  }
}
window.addEventListener('keydown', onWindowKeydown)

onBeforeUnmount(() => {
  observer?.disconnect()
  window.removeEventListener('keydown', onWindowKeydown)
})

// Escape answers the question about unsaved text (keep editing); it never leaves the page.
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && !e.defaultPrevented && confirmLeave.value) {
    e.preventDefault()
    view.keepEditing()
  }
}

function toggleDetails() {
  prefs.details = !prefs.details
}
</script>

<template>
  <article ref="root" class="tpage" :class="{ 'tpage--details': detailsBeside }" :aria-labelledby="titleId" data-ticket-page :data-ticket="number" @keydown="onKeydown">

    <header class="tpage__head">
      <div class="tpage__head-top">
        <TicketHeader v-if="ticket" ref="header" :ticket="ticket" :title-id="titleId" :level="1" @dirty="view.titleDirty.value = $event" />
        <div v-else class="min-w-0">
          <p class="tpage__eyebrow">#{{ number }}</p>
          <h1 :id="titleId" class="tpage__title" tabindex="-1" data-page-heading>Ticket #{{ number }}</h1>
        </div>
        <div class="tpage__controls">
          <button
            v-if="wide && ticket"
            type="button"
            class="omo-btn omo-btn--sm"
            :aria-expanded="prefs.details ? 'true' : 'false'"
            data-toggle-details
            @click="toggleDetails"
          >
            {{ prefs.details ? 'Hide details' : 'Show details' }}
          </button>
          <RouterLink
            :to="panelTarget"
            class="omo-btn omo-btn--sm"
            :title="`Open in panel (${SWITCH_VIEW_KEY})`"
            :aria-keyshortcuts="SWITCH_VIEW_KEY"
            data-open-panel
            >Open in panel</RouterLink
          >
        </div>
      </div>
      <TicketActions v-if="ticket" :ticket="ticket" />
      <TicketLeaveAsk v-if="confirmLeave" :id="`${titleId}-leave`" ref="ask" @keep="view.keepEditing()" @discard="view.discardAndLeave()" />
    </header>

    <div v-if="!ticket" class="tpage__notice">
      <TicketLoadNotice :view="view">
        <template #back="{ primary }">
          <RouterLink :to="boardTarget" class="omo-btn" :class="{ 'omo-btn--primary': primary }">Back to the board</RouterLink>
        </template>
      </TicketLoadNotice>
    </div>

    <div v-else class="tpage__body">
      <div class="tpage__main">
        <h2 class="sr-only">Activity</h2>
        <TicketTabs :ticket="ticket" overview>
          <template #overview>
            <div class="tpage__overview">
              <TicketDescription :view="view" :ticket="ticket" :heading-id="`${titleId}-desc`" />
              <section v-if="!detailsBeside" class="tpage__inline-fields" :aria-labelledby="`${titleId}-fields-inline`" data-page-fields="inline">
                <h3 :id="`${titleId}-fields-inline`" class="tpage__h">Details</h3>
                <TicketFields :ticket="ticket" />
              </section>
            </div>
          </template>
        </TicketTabs>
      </div>
      <aside v-if="detailsBeside" class="tpage__side" :aria-labelledby="`${titleId}-fields`" data-page-fields="side">
        <h2 :id="`${titleId}-fields`" class="tpage__h">Details</h2>
        <TicketFields :ticket="ticket" />
      </aside>
    </div>
  </article>
</template>

<style scoped>
/* Fills the board's place; head and nav keep their height, the body takes the rest and its parts scroll on their own. */
.tpage {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  height: 100%;
  min-height: 0;
  min-width: 0;
}
.tpage__head-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 4);
}
.tpage__head-top > :first-child {
  flex: 1 1 auto;
  min-width: 0;
}
.tpage__controls {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2);
}
.tpage__head {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 4) calc(var(--omo-space) * 5);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.tpage__eyebrow {
  margin: 0 0 var(--omo-space);
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tpage__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  line-height: var(--omo-text-xl-lh);
}
.tpage__title:focus,
.tpage__head :deep([data-ticket-title]:focus) {
  /* The heading takes the focus on arrival so a screen reader starts there; it is no control, so no frame. */
  outline: none;
}
.tpage__notice {
  padding: calc(var(--omo-space) * 5);
}
.tpage__body {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: 0;
  border: var(--omo-border) solid var(--omo-line);
  border-top: 0;
  background: var(--omo-surface);
}
.tpage--details .tpage__body {
  grid-template-columns: minmax(0, 1fr) minmax(16rem, 20rem);
}
.tpage__main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 5) 0;
}
.tpage__main > :deep(.ptabs) {
  flex: 1 1 auto;
}
.tpage__overview {
  display: grid;
  align-content: start;
  gap: calc(var(--omo-space) * 8);
  max-width: 880px;
}
.tpage__inline-fields {
  padding-top: calc(var(--omo-space) * 5);
  border-top: var(--omo-border) solid var(--omo-line);
}
.tpage__side {
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  padding: calc(var(--omo-space) * 5);
  border-left: var(--omo-border) solid var(--omo-line);
}
.tpage__h {
  display: flex;
  align-items: center;
  min-height: var(--omo-control-h-sm);
  margin: 0 0 calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
/* A phone: everything stacks and the page scrolls as one; the tabs stay right under the head. */
@media (max-width: 767.98px) {
  .tpage {
    display: block;
    overflow-y: auto;
  }
  .tpage__head {
    padding: calc(var(--omo-space) * 3);
  }
  /* The title gets the whole width; the view controls follow it. */
  .tpage__head-top {
    flex-direction: column;
    gap: calc(var(--omo-space) * 3);
  }
  .tpage__controls {
    justify-content: flex-start;
  }
  .tpage__main {
    padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3) 0;
  }
}
</style>
