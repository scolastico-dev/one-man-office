<script setup lang="ts">
// The ticket's place over the board, at `/<n>`: the head with the actions
// that apply now, the description (rendered, editable in place), the fields
// beside it (slot `fields`) and the tabs below (slot `tabs`). The ticket's own
// page (/ticket/<n>, "Open as page" or `p`) shows the same parts laid out
// for a whole page; what both share is in useTicketView and the components
// they both render.
//
// On a wide window the panel covers only the board's area (right of the
// sidebar, below the top bar) and is not modal: the sidebar and the top bar
// stay usable, the focus may leave the panel and come back, and only the
// columns under it are inert (the board does that). Escape closes it while
// the focus is inside and nothing inside took the key. On a phone it fills
// the screen as a modal dialog, as the sidebar is a sheet there.
//
// The panel does not close while there is unsaved text without asking.
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ticketPageLocation } from '../../locations'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import { closeDialog, showModal } from '../../components/ui/modalLayer'
import IconButton from '../../components/ui/IconButton.vue'
import UiButton from '../../components/ui/UiButton.vue'
import TicketActions from './TicketActions.vue'
import TicketDescription from './TicketDescription.vue'
import TicketFields from './TicketFields.vue'
import TicketHeader from './TicketHeader.vue'
import TicketLeaveAsk from './TicketLeaveAsk.vue'
import TicketLoadNotice from './TicketLoadNotice.vue'
import TicketTabs from './tabs/TicketTabs.vue'
import { panelTabOf } from './tabs/ticketTabs'
import { useTicketView } from './useTicketView'
import { SWITCH_VIEW_KEY } from './viewKeys'
import { modalOpen, plainKey } from '../../components/ui/shortcuts'

const props = defineProps<{ number: number }>()
const emit = defineEmits<{ close: [] }>()

const route = useRoute()
const router = useRouter()
const titleId = useId()

const dialog = ref<HTMLDialogElement>()
const header = ref<InstanceType<typeof TicketHeader>>()
const ask = ref<InstanceType<typeof TicketLeaveAsk>>()

const view = useTicketView(() => props.number, {
  root: dialog,
  discardTitle: () => header.value?.discard(),
  focusQuestion: () => ask.value?.focus(),
})
const { ticket, confirmLeave } = view

/**
 * The ticket's own page, on the tab shown here (Comments too: the page opens on its Overview otherwise);
 * while the description is being edited, on Overview, where the edit goes on.
 */
const pageTarget = computed(() => ticketPageLocation(props.number, route, view.editing.value ? {} : { tab: panelTabOf(route.query.tab) }))

let opener: HTMLElement | null = null

/** Below this width the panel fills the screen as a modal dialog. */
const NARROW = '(max-width: 767.98px)'
const media = typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(NARROW) : null
const modal = ref(media?.matches ?? false)

/** Shows the dialog the way the window's width asks for: modal on a phone, in the board's area otherwise. */
let shownModal = false
function present(el: HTMLDialogElement) {
  shownModal = modal.value
  if (shownModal) showModal(el)
  else el.show()
}

/** Set while the panel is closed only to be shown again the other way. */
let reshowing = false

// Crossing the phone's width: the panel is shown again the other way, with the focus where it was.
function onMedia() {
  modal.value = media?.matches ?? false
  const el = dialog.value
  if (!el?.open || shownModal === modal.value) return
  const at = document.activeElement
  reshowing = true
  closeDialog(el)
  present(el)
  reshowing = false
  if (at instanceof HTMLElement && el.contains(at)) at.focus()
}
media?.addEventListener('change', onMedia)

// While the board waits under the lock screen the panel steps aside (closed
// without closing the ticket), so the lock screen can be used, and comes back
// after, like every other dialog.
const suspended = inject(BOARD_SUSPENDED, null)
let parked = false
if (suspended) {
  watch(suspended, (s) => {
    const el = dialog.value
    if (!el) return
    if (s && el.open) {
      parked = true
      closeDialog(el)
    } else if (!s && parked) {
      parked = false
      if (el.isConnected && !el.open) present(el)
    }
  })
}

onMounted(() => {
  const el = dialog.value
  if (!el) return
  const active = document.activeElement
  opener = active instanceof HTMLElement && active !== document.body ? active : null
  if (suspended?.value) parked = true
  else if (!el.open) present(el)
  // As other dialogs without a field to fill in: the focus starts on Close, never on the (editable) title.
  void nextTick(() => el.querySelector<HTMLElement>('[data-close-panel]')?.focus())
})

/** Set while the panel goes away: closing its dialog then is not a request to close. */
let unmounting = false

onBeforeUnmount(() => {
  media?.removeEventListener('change', onMedia)
  unmounting = true
  const el = dialog.value
  if (el?.open) closeDialog(el)
  const back = opener
  const n = props.number
  // Focus goes back where the user came from, else to the ticket's card (none when the page took over).
  queueMicrotask(() => {
    const active = document.activeElement
    if (active && active !== document.body && active.isConnected) return
    const target = back?.isConnected ? back : document.querySelector<HTMLElement>(`li[data-ticket="${n}"] [data-card-link]`)
    target?.focus()
  })
})

function requestClose() {
  view.leave(() => emit('close'))
}

function openPage() {
  void router.push(pageTarget.value)
}

function onKeydown(e: KeyboardEvent) {
  if (plainKey(e, SWITCH_VIEW_KEY) && !confirmLeave.value && e.currentTarget === dialog.value && !modalOpen(dialog.value)) {
    e.preventDefault()
    openPage()
    return
  }
  if (e.key !== 'Escape' || e.defaultPrevented) return
  e.preventDefault()
  if (confirmLeave.value) view.keepEditing()
  else requestClose()
}

// The browser's own close request (a modal panel on a phone): the panel decides.
function onCancel(e: Event) {
  e.preventDefault()
  requestClose()
}

// Closed by the browser regardless: stay open while there is unsaved text.
function onDialogClose() {
  const el = dialog.value
  // Open again (the close event comes later): it was only shown anew for another width.
  if (!el || !el.isConnected || el.open || reshowing || unmounting || view.leaving() || parked) return
  if (view.dirty.value) {
    present(el)
    requestClose()
  } else emit('close')
}
</script>

<template>
  <!-- The board's area: the panel stands in it, over the (inert) columns. -->
  <div class="tpanel-area" :class="{ 'tpanel-area--modal': modal }" data-ticket-area>
    <dialog
      ref="dialog"
      class="tpanel"
      :aria-labelledby="titleId"
      :aria-modal="modal ? 'true' : undefined"
      data-ticket-panel
      @keydown="onKeydown"
      @cancel="onCancel"
      @close="onDialogClose"
    >
      <header class="tpanel__head">
        <div class="tpanel__head-top">
          <TicketHeader v-if="ticket" ref="header" :ticket="ticket" :title-id="titleId" @dirty="view.titleDirty.value = $event" />
          <div v-else class="min-w-0">
            <p class="tpanel__eyebrow">#{{ number }}</p>
            <h2 :id="titleId" class="tpanel__title" tabindex="-1" data-panel-heading>Ticket #{{ number }}</h2>
          </div>
          <div class="tpanel__controls">
            <RouterLink
              :to="pageTarget"
              class="omo-btn omo-btn--icon omo-btn--ghost omo-btn--md"
              aria-label="Open as page"
              :title="`Open as page (${SWITCH_VIEW_KEY})`"
              :aria-keyshortcuts="SWITCH_VIEW_KEY"
              data-open-page
            >
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
                <path d="M5 1.5H1.5v9h9V7M7 1.5h3.5V5M10.5 1.5 5.5 6.5" />
              </svg>
            </RouterLink>
            <IconButton label="Close" class="tpanel__close" data-close-panel @click="requestClose">
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
              </svg>
            </IconButton>
          </div>
        </div>
        <TicketActions v-if="ticket" :ticket="ticket" />
        <TicketLeaveAsk v-if="confirmLeave" :id="`${titleId}-leave`" ref="ask" @keep="view.keepEditing()" @discard="view.discardAndLeave()" />
      </header>

      <div class="tpanel__scroll">
        <TicketLoadNotice v-if="!ticket" :view="view">
          <template #back="{ primary }">
            <UiButton :variant="primary ? 'primary' : undefined" @click="emit('close')">Back to the board</UiButton>
          </template>
        </TicketLoadNotice>

        <div v-else class="tpanel__grid">
          <div class="tpanel__main">
            <TicketDescription :view="view" :ticket="ticket" :heading-id="`${titleId}-desc`" />
          </div>
          <aside class="tpanel__side" :aria-labelledby="`${titleId}-fields`">
            <h3 :id="`${titleId}-fields`" class="tpanel__h3">Details</h3>
            <slot name="fields" :ticket="ticket">
              <TicketFields :ticket="ticket" />
            </slot>
          </aside>
        </div>
        <slot v-if="ticket" name="tabs" :ticket="ticket"><TicketTabs :ticket="ticket" /></slot>
      </div>
    </dialog>
  </div>
</template>

<style scoped>
/*
 * The board's area (the shell's main, which is positioned): dimmed like a
 * dialog's backdrop, but only there; the sidebar and the top bar stay as they are.
 */
.tpanel-area {
  position: absolute;
  z-index: 20;
  inset: 0;
  display: flex;
  padding: calc(var(--omo-space) * 4);
  background: var(--omo-backdrop);
}
/* On a phone the modal panel is drawn above everything (the top layer) with its own backdrop. */
.tpanel-area--modal {
  display: contents;
}
.tpanel {
  position: relative;
  inset: auto;
  width: 100%;
  max-width: 1120px;
  height: 100%;
  max-height: none;
  margin: 0 auto;
  padding: 0;
  overflow: hidden;
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  color: var(--omo-fg);
}
.tpanel[open] {
  display: flex;
  flex-direction: column;
}
.tpanel::backdrop {
  background: var(--omo-backdrop);
}
.tpanel__head {
  display: grid;
  flex: none;
  gap: calc(var(--omo-space) * 3);
  max-height: 50dvh;
  overflow-y: auto;
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 6) calc(var(--omo-space) * 4);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.tpanel__head-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 4);
}
.tpanel__head-top > :first-child {
  flex: 1 1 auto;
  min-width: 0;
}
.tpanel__controls {
  display: flex;
  flex-shrink: 0;
  gap: var(--omo-space);
  margin: calc(var(--omo-space) * -1.5) calc(var(--omo-space) * -2) 0 0;
}
.tpanel__eyebrow {
  margin: 0 0 var(--omo-space);
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tpanel__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  line-height: var(--omo-text-xl-lh);
}
.tpanel__title:focus {
  outline: none;
}
.tpanel__scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 6) calc(var(--omo-space) * 6);
}
.tpanel__grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(14rem, 18rem);
  gap: calc(var(--omo-space) * 8);
  align-items: start;
}
.tpanel__main,
.tpanel__side {
  min-width: 0;
}
.tpanel__side {
  padding-left: calc(var(--omo-space) * 6);
  border-left: var(--omo-border) solid var(--omo-line);
}
.tpanel__h3 {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.tpanel__side .tpanel__h3 {
  display: flex;
  align-items: center;
  min-height: var(--omo-control-h-sm);
  margin-bottom: calc(var(--omo-space) * 3);
}
@media (max-width: 767.98px) {
  .tpanel {
    position: fixed;
    inset: 0;
    width: 100%;
    max-width: none;
    height: 100dvh;
    margin: 0;
    border: 0;
  }
  .tpanel__head {
    max-height: 45dvh;
    padding: calc(var(--omo-space) * 4) calc(var(--omo-space) * 4) calc(var(--omo-space) * 3);
  }
  .tpanel__scroll {
    padding: calc(var(--omo-space) * 4);
  }
  .tpanel__grid {
    grid-template-columns: minmax(0, 1fr);
    gap: calc(var(--omo-space) * 6);
  }
  .tpanel__side {
    padding: calc(var(--omo-space) * 5) 0 0;
    border-top: var(--omo-border) solid var(--omo-line);
    border-left: 0;
  }
}
</style>
