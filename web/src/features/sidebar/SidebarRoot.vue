<script setup lang="ts">
// The sidebar of the board page: a column on the left that is a strip of
// vertical tabs when collapsed and the tabs with their content when expanded,
// with a splitter to set its width. Below 768 px there is no column: the same
// tabs show in a full-screen sheet that the top bar's Sidebar button opens.
// A ticket's panel opens beside it (in the board's area), so the sidebar and
// its terminals stay usable while a panel is open.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import IconButton from '../../components/ui/IconButton.vue'
import Splitter from '../../components/ui/Splitter.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import UiTabs, { type UiTab } from '../../components/ui/UiTabs.vue'
import { useBoard } from '../../store'
import SidebarPlugins from '../plugins/SidebarPlugins.vue'
import PluginSidebarTab from '../plugins/PluginSidebarTab.vue'
import { usePluginExtensions } from '../plugins/usePluginExtensions'
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
const { runtime } = usePluginExtensions()
const pluginTabId = (t: { plugin: string; id: string }) => `plugin:${t.plugin}:${t.id}`
const pluginTab = (id: string) => runtime.tabs.value.find(t => pluginTabId(t) === id)

const tabs = computed<UiTab[]>(() =>
  [...visibleTabs({ ceoEnabled: store.state.general?.ceo.enabled ?? false }).map((t) => {
    const count = t.id === 'agents' ? agents.sessions.value.length : t.id === 'terminals' ? terminals.count.value : undefined
    return { id: t.id, label: t.label, ...(t.hint ? { hint: t.hint } : {}), ...(count === undefined ? {} : { count }) }
  }), ...runtime.tabs.value.map(t => ({ id: pluginTabId(t), label: t.label }))],
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

// On a phone a ticket's panel fills the screen: a ticket opened from the sheet replaces it.
watch(
  () => route.name === 'ticket',
  (open) => {
    if (open && sidebar.narrow.value) sidebar.hide()
  },
)

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
    data-sidebar
    :data-open="sidebar.open.value"
    @keydown.capture="keyboardUser = true"
    @pointerdown.capture="keyboardUser = false"
  >
    <div class="side" :class="{ 'side--open': sidebar.open.value }" :style="sidebar.open.value ? { width: `${width}px` } : undefined">
      <div class="side__head omo-compact">
        <IconButton ref="toggleButton" :label="sidebar.open.value ? 'Collapse sidebar' : 'Expand sidebar'" :aria-expanded="sidebar.open.value" data-sidebar-toggle @click="sidebar.toggle()">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
            <path v-if="sidebar.open.value" d="M7.5 2 3.5 6l4 4M10.5 2v8" />
            <path v-else d="M4.5 2l4 4-4 4M1.5 2v8" />
          </svg>
        </IconButton>
      </div>
      <div v-if="sidebar.open.value" class="side__body">
        <UiTabs :model-value="tab" class="side__tabs" :tabs="tabs" label="Sidebar" fill @update:model-value="sidebar.setTab($event, 'user')">
          <template #panel="{ active }">
            <PluginSidebarTab v-if="pluginTab(active)" :key="active" :tab="pluginTab(active)!" />
            <component v-else :is="content(active).component" v-bind="content(active).props?.(sidebar)" />
          </template>
        </UiTabs>
        <SidebarPlugins />
      </div>
      <!-- Collapsed, no tab is shown, so none is marked as selected. -->
      <UiTabs v-else model-value="" class="side__tabs" :tabs="tabs" label="Sidebar" orientation="vertical" @update:model-value="pick" />
    </div>
    <Splitter v-if="sidebar.open.value" :model-value="width" :min="MIN_WIDTH" :max="maxWidth" label="Sidebar width" @update:model-value="sidebar.width.value = $event" />
  </aside>

  <UiDialog v-if="sidebar.narrow.value" class="side-sheet" :open="sidebar.sheet.value" title="Agents and terminals" @close="sidebar.hide()">
    <!-- Only while shown: a hidden sheet keeps no terminal or socket open. -->
    <UiTabs v-if="sidebar.sheet.value" ref="sheetTabs" class="side-sheet__tabs" :model-value="tab" :tabs="tabs" label="Sidebar" fill @update:model-value="sidebar.setTab($event, 'user')">
      <template #panel="{ active }">
        <PluginSidebarTab v-if="pluginTab(active)" :key="active" :tab="pluginTab(active)!" />
        <component v-else :is="content(active).component" v-bind="content(active).props?.(sidebar)" />
      </template>
    </UiTabs>
    <SidebarPlugins v-if="sidebar.sheet.value" />
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
.side__body { display: flex; flex-direction: column; min-height: 0; }
.side__tabs { flex: 1 1 auto; min-height: 0; }
.side__body > :deep(.sidebar-plugins) { flex: none; }
.side__tabs :deep([role='tabpanel']) {
  overflow-y: auto;
}
/* The splitter's rule is the sidebar's right edge. */
.side-frame > :deep([role='separator']) {
  grid-row: 1 / -1;
  margin-left: calc(var(--omo-space) * -1);
}
</style>

<style>
/* The phone's sheet: the tabs take the sheet's height, so an open terminal fills it as it fills the column. */
.side-sheet[open] > .dialog__body {
  display: flex;
  flex-direction: column;
}
.side-sheet__tabs {
  flex: 1 1 auto;
}
.side-sheet > .dialog__body > .sidebar-plugins { flex: none; }
</style>
