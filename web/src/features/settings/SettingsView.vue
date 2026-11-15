<script setup lang="ts">
// The settings in place of the board (at /settings/<section>): the section
// list on the left and the section's page beside it. Below 768 px the list
// is a page of its own (/settings) and each section has a link back to it.
// /settings on a wide screen shows Projects; an address naming no section
// shows Projects with a notice. Each page is loaded when first shown.
import { computed, defineAsyncComponent, defineComponent, h, nextTick, onBeforeUnmount, provide, ref, watch, type Component } from 'vue'
import { useRoute } from 'vue-router'
import Callout from '../../components/ui/Callout.vue'
import { isSettingsSection, type SettingsSection } from '../../locations'
import SettingsNav from './SettingsNav.vue'
import { SETTINGS_FOCUS_TITLE, SETTINGS_NARROW, SETTINGS_NOTICE, SETTINGS_SECTIONS } from './settingsSections'
import { QuestionHost } from './useLeaveGuard'

/** Shown in place of a page whose code could not be loaded (for example after omo was updated). */
const LoadFailed = defineComponent({
  name: 'SettingsPageLoadFailed',
  setup: () => () =>
    h('section', { class: 'settings-view__failed', 'data-settings-page-failed': '' }, [
      h(Callout, { tone: 'danger', live: 'assertive' }, () => h('p', 'This page could not be loaded. Reload the browser tab to try again.')),
    ]),
})

const pages = new Map<SettingsSection, Component>(
  SETTINGS_SECTIONS.map((s) => [s.id, defineAsyncComponent({ loader: s.component, errorComponent: LoadFailed })]),
)

const route = useRoute()

const param = computed(() => {
  const p = route.params.section
  return typeof p === 'string' && p !== '' ? p : null
})
/** The section the address names; null for /settings and for a name that is no section. */
const section = computed<SettingsSection | null>(() => (isSettingsSection(param.value) ? param.value : null))
const unknown = computed(() => (param.value !== null && section.value === null ? param.value : null))
const shown = computed<SettingsSection>(() => section.value ?? 'projects')

const narrowQuery = window.matchMedia('(max-width: 767.98px)')
const narrow = ref(narrowQuery.matches)
const onNarrow = (e: MediaQueryListEvent) => (narrow.value = e.matches)
narrowQuery.addEventListener('change', onNarrow)
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onNarrow))

/** On a narrow screen /settings is the section list alone; any other address is a page alone. */
const listOnly = computed(() => narrow.value && param.value === null)
const showList = computed(() => !narrow.value || listOnly.value)
const showPage = computed(() => !listOnly.value)

const notice = computed(() => (unknown.value === null ? null : `There is no settings section “${unknown.value}”. This is Projects.`))
const focusTitle = ref(false)
provide(SETTINGS_NARROW, narrow)
provide(SETTINGS_NOTICE, notice)
provide(SETTINGS_FOCUS_TITLE, focusTitle)

// On a narrow screen the list and a page replace each other: the focus goes
// to the new page's title, or back to the entry of the section just left.
const side = ref<HTMLElement>()
let lastSection: SettingsSection | null = section.value
watch(section, (s) => {
  if (s !== null) lastSection = s
})
watch(listOnly, (now, before) => {
  if (!narrow.value) return
  if (before && !now) focusTitle.value = true
  else if (!before && now && lastSection !== null) {
    const left = lastSection
    void nextTick(() => side.value?.querySelector<HTMLElement>(`[data-section="${left}"]`)?.focus())
  }
})
</script>

<template>
  <div class="settings-view" :class="{ 'settings-view--narrow': narrow }" data-settings :data-settings-section="showPage ? shown : undefined">
    <div v-if="showList" ref="side" class="settings-view__side">
      <h1 v-if="listOnly" class="sr-only">Settings</h1>
      <SettingsNav :current="listOnly ? null : shown" />
    </div>
    <component :is="pages.get(shown)" v-if="showPage" :key="shown" />
    <QuestionHost />
  </div>
</template>

<style scoped>
.settings-view {
  display: grid;
  grid-template-columns: calc(var(--omo-space) * 64) minmax(0, 1fr);
  align-items: start;
  gap: calc(var(--omo-space) * 4);
  height: 100%;
  min-height: 0;
}
.settings-view--narrow {
  grid-template-columns: minmax(0, 1fr);
}
/* Scrolls on its own; the padding keeps the panel's label, which sits on the top border, in view. */
.settings-view__side {
  max-height: 100%;
  padding-top: calc(var(--omo-space) * 2.5);
  overflow-y: auto;
}
.settings-view :deep(.settings-view__failed) {
  padding-top: calc(var(--omo-space) * 2.5);
  max-width: 880px;
}
</style>
