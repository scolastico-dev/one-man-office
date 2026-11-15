<script setup lang="ts">
// The sidebar with static data: collapsed a strip of vertical tabs,
// expanded the tabs with their content and a splitter to set the width.
// Below 768 px the sample shows the same tabs in a full-screen sheet instead.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import IconButton from '../components/ui/IconButton.vue'
import Splitter from '../components/ui/Splitter.vue'
import UiTabs, { type UiTab } from '../components/ui/UiTabs.vue'
import { sampleAgents } from './sampleBoard'
import SampleSidebarPanel from './SampleSidebarPanel.vue'

const open = defineModel<boolean>('open', { required: true })
const tab = defineModel<string>('tab', { required: true })

const tabs = computed<UiTab[]>(() => [
  { id: 'ceo', label: 'CEO', hint: 'Starts the CEO when it is not running' },
  { id: 'agents', label: 'Agents', count: sampleAgents.length },
  { id: 'terminals', label: 'Terminals', count: 3 },
])

const REM = 16
const width = ref(30 * REM)
const viewport = ref(window.innerWidth)
const maxWidth = computed(() => Math.max(20 * REM, Math.floor(viewport.value * 0.6)))
const onResize = () => (viewport.value = window.innerWidth)
onMounted(() => window.addEventListener('resize', onResize))
onBeforeUnmount(() => window.removeEventListener('resize', onResize))

/** In the collapsed strip, choosing a tab opens the sidebar on it. */
function pick(id: string) {
  tab.value = id
  open.value = true
}
</script>

<template>
  <aside class="side-frame" aria-label="Sidebar">
    <div
      class="side"
      :class="{ 'side--open': open }"
      :style="open ? { width: `${Math.min(width, maxWidth)}px` } : undefined"
    >
      <div class="side__head">
        <IconButton :label="open ? 'Collapse sidebar' : 'Expand sidebar'" size="sm" @click="open = !open">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
            <path v-if="open" d="M7.5 2 3.5 6l4 4M10.5 2v8" />
            <path v-else d="M4.5 2l4 4-4 4M1.5 2v8" />
          </svg>
        </IconButton>
      </div>
      <UiTabs v-if="open" v-model="tab" class="side__tabs" :tabs="tabs" label="Sidebar">
        <template #panel="{ active }">
          <SampleSidebarPanel :tab="active" />
        </template>
      </UiTabs>
      <!-- Collapsed, no tab is shown, so none is marked as selected. -->
      <UiTabs v-else model-value="" :tabs="tabs" label="Sidebar" orientation="vertical" @update:model-value="pick" />
    </div>
    <Splitter v-if="open" v-model="width" :min="20 * REM" :max="maxWidth" label="Sidebar width" />
  </aside>
</template>

<style scoped>
.side-frame {
  display: flex;
  height: 100%;
}
.side {
  display: flex;
  flex-direction: column;
  width: calc(var(--omo-space) * 11);
  height: 100%;
  border-right: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.side--open {
  border-right: 0;
}
.side__head {
  display: flex;
  justify-content: center;
  padding: calc(var(--omo-space) * 2) 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.side--open .side__head {
  justify-content: flex-end;
  padding-right: calc(var(--omo-space) * 2);
}
.side__tabs {
  flex: 1 1 auto;
}
.side__tabs :deep([role='tabpanel']) {
  overflow-y: auto;
}
/* The splitter's rule is the sidebar's right edge. */
.side-frame > :deep([role='separator']) {
  margin-left: calc(var(--omo-space) * -1);
}
@media (max-width: 767.98px) {
  .side-frame {
    display: none;
  }
}
</style>
