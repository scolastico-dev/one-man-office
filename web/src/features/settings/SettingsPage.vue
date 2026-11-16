<script setup lang="ts">
// The frame of one settings page: the title and its line, room for actions
// beside the title, the content, and (below 768 px) the link back to the
// section list. Given a `resource` (one useSettingsResource item edited as a
// form), it also shows the item's broken files and a change made on disk,
// renders the content only once the item is read, ends with the Save bar
// (with "Use the new value" and "Keep mine" in a conflict) and asks before
// the page is left with unsaved changes. Pages with several forms leave
// `resource` unset and put a SaveBar per form themselves.
import { computed, inject, nextTick, onMounted, ref, useId } from 'vue'
import { useRoute } from 'vue-router'
import Callout from '../../components/ui/Callout.vue'
import SaveBar from '../../components/ui/SaveBar.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { settingsLocation } from '../../locations'
import FileErrors from './FileErrors.vue'
import { SETTINGS_FOCUS_TITLE, SETTINGS_NARROW, SETTINGS_NOTICE } from './settingsSections'
import { useLeaveGuard } from './useLeaveGuard'
import { diskChangeText, focusFirstInvalid, type SettingsResource } from './useSettingsResource'

const props = withDefaults(
  defineProps<{
    title: string
    /** One line under the title, without a final full stop. */
    description?: string
    resource?: SettingsResource<unknown>
  }>(),
  { description: undefined, resource: undefined },
)

const route = useRoute()
const narrow = inject(SETTINGS_NARROW, ref(false))
const notice = inject(SETTINGS_NOTICE, ref(null))
const focusTitle = inject(SETTINGS_FOCUS_TITLE, ref(false))
const titleId = useId()
const root = ref<HTMLElement>()
const heading = ref<HTMLElement>()

const r = computed(() => props.resource)
const ready = computed(() => r.value === undefined || r.value.draft.value !== null)
const loadFailed = computed(() => r.value !== undefined && !r.value.loading.value && r.value.value.value === null && r.value.error.value !== '')

useLeaveGuard(computed(() => r.value?.dirty.value ?? false))

onMounted(() => {
  if (!focusTitle.value) return
  focusTitle.value = false
  void nextTick(() => heading.value?.focus())
})

async function save() {
  const res = r.value
  if (res === undefined) return
  if ((await res.save()) === 'refused') await focusFirstInvalid(root.value)
}
</script>

<template>
  <section ref="root" class="settings-page" :aria-labelledby="titleId" data-settings-page>
    <div class="settings-page__column">
      <header class="settings-page__head">
        <RouterLink v-if="narrow" :to="settingsLocation(null, route)" class="settings-page__back" data-settings-back>
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M8 2L4 6l4 4" />
          </svg>
          Settings
        </RouterLink>
        <div class="settings-page__title-row">
          <div class="min-w-0">
            <h1 :id="titleId" ref="heading" class="settings-page__title" tabindex="-1">{{ title }}</h1>
            <p v-if="description" class="settings-page__desc">{{ description }}.</p>
          </div>
          <div v-if="$slots.actions" class="settings-page__actions"><slot name="actions" /></div>
        </div>
      </header>

      <div class="settings-page__body">
        <Callout v-if="notice" tone="info" data-settings-notice>
          <p>{{ visibleText(notice) }}</p>
        </Callout>
        <template v-if="r">
          <FileErrors :errors="r.broken.value" />
          <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
            <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
          </Callout>
          <p v-if="r.loading.value && !ready" class="settings-page__loading" data-settings-loading>Reading the settings…</p>
          <Callout v-else-if="loadFailed" tone="danger" live="assertive" data-settings-load-error>
            <p>{{ r.error.value }}</p>
            <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
          </Callout>
        </template>
        <slot v-if="ready" />
      </div>

      <SaveBar
        v-if="r && ready"
        :dirty="r.dirty.value"
        :busy="r.saving.value"
        :status="r.status.value"
        :error="r.error.value || undefined"
        @save="save"
        @discard="r.discard()"
      >
        <template v-if="r.status.value === 'conflict'">
          <UiButton size="sm" data-use-theirs @click="r.useTheirs()">Use the new value</UiButton>
          <UiButton size="sm" data-keep-mine @click="r.keepMine()">Keep mine</UiButton>
        </template>
      </SaveBar>
    </div>
  </section>
</template>

<style scoped>
/*
 * The page scrolls under a sticky Save bar: a field brought into view (by the
 * focus, by the browser) stops above the bar instead of under it. The room
 * kept is that of a bar on two rows with a line of error.
 */
.settings-page {
  height: 100%;
  padding-top: calc(var(--omo-space) * 2.5);
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
  scroll-padding-bottom: calc(var(--omo-space) * 28);
}
.settings-page__column {
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 5);
  max-width: 880px;
  min-height: 100%;
}
.settings-page__head {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
.settings-page__back {
  display: inline-flex;
  align-items: center;
  justify-self: start;
  gap: calc(var(--omo-space) * 1.5);
  min-height: var(--omo-control-h-sm);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-decoration: none;
}
.settings-page__title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3) calc(var(--omo-space) * 6);
}
.settings-page__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  line-height: var(--omo-text-xl-lh);
}
.settings-page__desc {
  margin: var(--omo-space) 0 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.settings-page__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.settings-page__body {
  display: grid;
  flex: 1 0 auto;
  align-content: start;
  gap: calc(var(--omo-space) * 6);
  min-width: 0;
  padding-top: calc(var(--omo-space) * 2);
}
.settings-page__loading {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
@media (hover: hover) {
  .settings-page__back:hover {
    color: var(--omo-accent);
  }
}
</style>
