<script setup lang="ts">
// The sidebar of the board page: a column on the left that is a strip of
// vertical tabs when collapsed and the tabs with their content when expanded,
// with a splitter to set its width. Below 768 px there is no column: the same
// tabs show in a full-screen sheet that the top bar's Sidebar button opens.
// It is inert while a ticket's panel is open over it.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import IconButton from '../../components/ui/IconButton.vue'
import Splitter from '../../components/ui/Splitter.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import UiTabs, { type UiTab } from '../../components/ui/UiTabs.vue'
import { useBoard } from '../../store'
import { registerSidebarActions } from './sidebarActions'
import { sidebarTabs, visibleTabs } from './sidebarTabs'
import { MAX_SHARE, MIN_WIDTH, NARROW_QUERY, useSidebar } from './useSidebar'
import { useAgents } from './useAgents'
import { useTerminals } from './useTerminals'

const sidebar = useSidebar()
const store = useBoard()
const route = useRoute()
const agents = useAgents()
const terminals = useTerminals()

const tabs = computed<UiTab[]>(() =>
  visibleTabs({ ceoEnabled: store.state.general?.ceo.enabled ?? false }).map((t) => {
    const count = t.id === 'agents' ? agents.sessions.value.length : t.id === 'terminals' ? terminals.count.value : undefined
    return { id: t.id, label: t.label, ...(t.hint ? { hint: t.hint } : {}), ...(count === undefined ? {} : { count }) }
  }),
)
const content = (id: string) => sidebarTabs.find((t) => t.id === id) ?? sidebarTabs[1]!
/** The remembered tab, unless it is hidden now (the CEO tab while the CEO is off). */
const tab = computed(() => (tabs.value.some((t) => t.id === sidebar.tab.value) ? sidebar.tab.value : 'agents'))

const viewport = ref(typeof window === 'undefined' ? 1440 : window.innerWidth)
const maxWidth = computed(() => Math.max(MIN_WIDTH, Math.floor(viewport.value * MAX_SHARE)))
const width = computed(() => Math.min(sidebar.width.value, maxWidth.value))

let media: MediaQueryList | null = null
const onMedia = () => sidebar.setNarrow(media?.matches ?? false)
const onResize = () => (viewport.value = window.innerWidth)
onMounted(() => {
  registerSidebarActions()
  window.addEventListener('resize', onResize)
  if (typeof window.matchMedia === 'function') {
    media = window.matchMedia(NARROW_QUERY)
    media.addEventListener?.('change', onMedia)
    onMedia()
  }
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  media?.removeEventListener?.('change', onMedia)
})

/** A ticket's panel covers the page: the sidebar behind it takes no input. */
const covered = computed(() => route.name === 'ticket')
// A ticket opened from the sheet replaces it.
watch(covered, (c) => {
  if (c && sidebar.narrow.value) sidebar.hide()
})
watch(covered, (c) => sidebar.setCovered(c), { immediate: true })
onBeforeUnmount(() => sidebar.setCovered(false))

// Whether the last input was a key, so only a keyboard user is moved into the sidebar.
const keyboardUser = ref(false)

const frame = ref<HTMLElement>()
const toggleButton = ref<InstanceType<typeof IconButton>>()

function focusSelectedTab() {
  const el = frame.value?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')
  el?.focus()
}

function pick(id: string) {
  sidebar.setTab(id, 'user')
  sidebar.show()
}

watch(
  () => sidebar.open.value,
  async (open) => {
    if (sidebar.narrow.value) return
    await nextTick()
    if (open) {
      if (keyboardUser.value) focusSelectedTab()
      return
    }
    // The collapsed strip replaced what had the focus: it goes to the button that collapses and expands.
    const at = document.activeElement
    if (at === null || at === document.body || frame.value?.contains(at)) (toggleButton.value?.$el as HTMLElement | undefined)?.focus()
  },
)
// The sheet opens on its selected tab, not on whatever the dialog finds first
// (a terminal's input). This runs after the dialog's own first focus.
const sheetTabs = ref<InstanceType<typeof UiTabs>>()
watch(
  () => sidebar.sheet.value,
  async (open) => {
    if (!open || !sidebar.narrow.value) return
    await nextTick()
    ;(sheetTabs.value?.$el as HTMLElement | undefined)?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus()
  },
  { flush: 'post' },
)
// A feature asked to open the sidebar on something: the focus goes in.
watch(
  () => sidebar.focusRequests.value,
  async () => {
    if (sidebar.narrow.value) return
    await nextTick()
    focusSelectedTab()
  },
)
</script>

<template>
  <aside
    v-if="!sidebar.narrow.value"
    ref="frame"
    class="side-frame"
    aria-label="Sidebar"
    :inert="covered || undefined"
    data-sidebar
    :data-open="sidebar.open.value"
    @keydown.capture="keyboardUser = true"
    @pointerdown.capture="keyboardUser = false"
  >
    <div class="side" :class="{ 'side--open': sidebar.open.value }" :style="sidebar.open.value ? { width: `${width}px` } : undefined">
      <div class="side__head">
        <IconButton ref="toggleButton" :label="sidebar.open.value ? 'Collapse sidebar' : 'Expand sidebar'" :aria-expanded="sidebar.open.value" data-sidebar-toggle @click="sidebar.toggle()">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
            <path v-if="sidebar.open.value" d="M7.5 2 3.5 6l4 4M10.5 2v8" />
            <path v-else d="M4.5 2l4 4-4 4M1.5 2v8" />
          </svg>
        </IconButton>
      </div>
      <UiTabs v-if="sidebar.open.value" :model-value="tab" class="side__tabs" :tabs="tabs" label="Sidebar" @update:model-value="sidebar.setTab($event, 'user')">
        <template #panel="{ active }">
          <component :is="content(active).component" v-bind="content(active).props?.(sidebar)" />
        </template>
      </UiTabs>
      <!-- Collapsed, no tab is shown, so none is marked as selected. -->
      <UiTabs v-else model-value="" class="side__tabs" :tabs="tabs" label="Sidebar" orientation="vertical" @update:model-value="pick" />
    </div>
    <Splitter v-if="sidebar.open.value" :model-value="width" :min="MIN_WIDTH" :max="maxWidth" label="Sidebar width" @update:model-value="sidebar.width.value = $event" />
  </aside>

  <UiDialog v-if="sidebar.narrow.value" :open="sidebar.sheet.value" title="Sidebar" @close="sidebar.hide()">
    <!-- Only while shown: a hidden sheet keeps no terminal or socket open. -->
    <UiTabs v-if="sidebar.sheet.value" ref="sheetTabs" :model-value="tab" :tabs="tabs" label="Sidebar" @update:model-value="sidebar.setTab($event, 'user')">
      <template #panel="{ active }">
        <component :is="content(active).component" v-bind="content(active).props?.(sidebar)" />
      </template>
    </UiTabs>
  </UiDialog>
</template>

<style scoped>
/*
 * The frame and the column use the page's two rows (the shell's subgrid):
 * the head fills the top bar's row, the tabs the rest.
 */
.side-frame {
  display: grid;
  grid-template-rows: subgrid;
  grid-template-columns: auto auto;
  grid-row: 1 / -1;
  height: 100%;
}
.side {
  display: grid;
  grid-template-rows: subgrid;
  grid-row: 1 / -1;
  width: calc(var(--omo-space) * 11);
  min-height: 0;
  border-right: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.side--open {
  border-right: 0;
}
/* The button stands in the middle of the top bar's height, like the bar's own controls; the head's line is the top bar's. */
.side__head {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: calc(var(--omo-space) * 2) 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.side--open .side__head {
  justify-content: flex-end;
  padding-right: calc(var(--omo-space) * 2);
}
.side__tabs {
  min-height: 0;
}
.side__tabs :deep([role='tabpanel']) {
  overflow-y: auto;
}
/* The splitter's rule is the sidebar's right edge. */
.side-frame > :deep([role='separator']) {
  grid-row: 1 / -1;
  margin-left: calc(var(--omo-space) * -1);
}
</style>
