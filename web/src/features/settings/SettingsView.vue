<script setup lang="ts">
// The settings in place of the board (at /settings/<section>): the section
// list on the left and the section's page beside it. Below 768 px the list
// is a page of its own (/settings) and each section has a link back to it.
// /settings on a wide screen shows Projects; an address naming no section
// shows Projects with a notice. Each page is loaded when first shown.
import { computed, defineComponent, h, nextTick, onBeforeUnmount, provide, ref, watch, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ChunkFailed, FAILED_PAGE_TITLE, lazyComponent } from '../../components/ui/chunkFailure'
import { isSettingsSection, settingsLocation, type SettingsSection } from '../../locations'
import SettingsNav from './SettingsNav.vue'
import { SETTINGS_FOCUS_TITLE, SETTINGS_NARROW, SETTINGS_NOTICE, SETTINGS_SECTIONS, settingsLayout, shownSection } from './settingsSections'
import { anyDirty, QuestionHost } from './useLeaveGuard'
import '../../layout/settingsFrame.css'

/** Shown in place of a page whose code could not be loaded (for example after omo was updated), with Reload. */
const LoadFailed = defineComponent({
  name: 'SettingsPageLoadFailed',
  setup: () => () => h('section', { class: 'settings-view__failed', 'data-settings-page-failed': '' }, [h('h1', { class: 'sr-only' }, FAILED_PAGE_TITLE), h(ChunkFailed)]),
})

const pages = new Map<SettingsSection, Component>(SETTINGS_SECTIONS.map((s) => [s.id, lazyComponent(s.component, { errorComponent: LoadFailed })]))

const route = useRoute()

const param = computed(() => {
  const p = route.params.section
  return typeof p === 'string' && p !== '' ? p : null
})
/** The section the address names; null for /settings and for a name that is no section. */
const section = computed<SettingsSection | null>(() => (isSettingsSection(param.value) ? param.value : null))
const unknown = computed(() => (param.value !== null && section.value === null ? param.value : null))
const shown = computed<SettingsSection>(() => shownSection(param.value, false)!)

const router = useRouter()
const narrowQuery = window.matchMedia('(max-width: 767.98px)')
const narrow = ref(narrowQuery.matches)
settingsLayout.narrow = narrow.value
watch(narrow, (n) => (settingsLayout.narrow = n))
// Narrowing at /settings would hide Projects behind the section list: a page
// with unsaved changes moves to its own address first and stays on screen.
async function onNarrow(e: MediaQueryListEvent) {
  if (e.matches && !narrow.value && param.value === null && anyDirty.value) {
    await router.replace(settingsLocation(shown.value, route))
  }
  narrow.value = e.matches
}
narrowQuery.addEventListener('change', onNarrow)
onBeforeUnmount(() => {
  narrowQuery.removeEventListener('change', onNarrow)
  settingsLayout.narrow = false
})

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
  <div class="settings-view settings-frame" :class="{ 'settings-view--narrow': narrow }" data-settings :data-settings-section="showPage ? shown : undefined">
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
  grid-template-columns: var(--settings-list) minmax(0, 1fr);
  align-items: start;
  gap: var(--settings-gap);
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
.settings-view--narrow .settings-view__side {
  padding-right: var(--settings-edge);
}
.settings-view :deep(.settings-view__failed) {
  padding-top: calc(var(--omo-space) * 2.5);
  margin-right: var(--settings-edge);
  max-width: var(--settings-page-max);
}
</style>
