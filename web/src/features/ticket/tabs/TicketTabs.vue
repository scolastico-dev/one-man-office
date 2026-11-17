<script setup lang="ts">
// The tabs of a ticket, in its panel and on its page. The selected tab lives
// in the address query (`?tab=diff`; none for the default); switching keeps
// every other key (the board's filters stay). Only the selected tab's panel
// exists, so the others hold no connection and send no request.
//
// In the panel the tabs sit under the description and Comments is the
// default. On the page (`overview`) the tabs stand at the top, a first tab
// Overview (the slot `overview`) is the default, and the selected tab's
// content fills the height left.
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { TicketSummary } from '../../../api/types'
import UiTabs, { type UiTab } from '../../../components/ui/UiTabs.vue'
import { DEFAULT_PANEL_TAB, OVERVIEW_TAB, ticketTabs } from './ticketTabs'

const props = withDefaults(
  defineProps<{
    ticket: TicketSummary
    /** The page's layout: a first tab Overview, shown by default, and the content filling the height. */
    overview?: boolean
  }>(),
  { overview: false },
)
defineSlots<{ overview?(): unknown }>()

const route = useRoute()
const router = useRouter()

const available = computed(() => ticketTabs(props.ticket))
const uiTabs = computed<UiTab[]>(() => {
  const list: UiTab[] = available.value.map((t) => {
    const count = t.count?.(props.ticket)
    return count === undefined ? { id: t.id, label: t.label } : { id: t.id, label: t.label, count }
  })
  return props.overview ? [{ id: OVERVIEW_TAB, label: 'Overview' }, ...list] : list
})

const defaultTab = computed(() => (props.overview ? OVERVIEW_TAB : DEFAULT_PANEL_TAB))

const active = computed(() => {
  const q = route.query.tab
  const want = Array.isArray(q) ? q[0] : q
  return uiTabs.value.find((t) => t.id === want)?.id ?? defaultTab.value
})

const current = computed(() => available.value.find((t) => t.id === active.value))

function select(id: string) {
  const query = { ...route.query }
  delete query.comment
  if (id === defaultTab.value) delete query.tab
  else query.tab = id
  void router.replace({ query })
}
</script>

<template>
  <section class="ptabs" :class="{ 'ptabs--page': overview }" aria-label="Activity" :data-ticket-tabs="overview ? 'page' : 'panel'">
    <UiTabs :model-value="active" :tabs="uiTabs" :label="`Activity on #${ticket.number}`" @update:model-value="select">
      <template #panel>
        <div class="ptabs__panel" :data-tab-content="active">
          <slot v-if="active === OVERVIEW_TAB" name="overview" />
          <component :is="current.component" v-else-if="current" :key="current.id" :ticket="ticket" />
        </div>
      </template>
    </UiTabs>
  </section>
</template>

<style scoped>
.ptabs {
  margin-top: calc(var(--omo-space) * 8);
}
.ptabs__panel {
  padding-top: calc(var(--omo-space) * 5);
}
/* On the page the tabs head the column and the content takes the height left; it scrolls on its own. */
.ptabs--page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  margin-top: 0;
}
.ptabs--page > :deep(.tabs) {
  flex: 1 1 auto;
  min-height: 0;
}
.ptabs--page :deep(.tabs__panel) {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
}
.ptabs--page .ptabs__panel {
  display: flex;
  flex: 1 0 auto;
  flex-direction: column;
  min-height: 0;
  padding: calc(var(--omo-space) * 5) 0 calc(var(--omo-space) * 4);
}
/* A phone: each tab as wide as its words, so five of them still read in full; the list scrolls on its own if not. */
@media (max-width: 767.98px) {
  .ptabs--page :deep(.tabs__list) {
    overflow-x: auto;
  }
  .ptabs--page :deep(.tabs__tab) {
    flex: 1 0 auto;
    padding-right: calc(var(--omo-space) * 1.5);
    padding-left: calc(var(--omo-space) * 1.5);
  }
}
</style>
