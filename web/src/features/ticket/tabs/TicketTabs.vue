<script setup lang="ts">
// The tabs of the ticket panel. The selected tab lives in the address query
// (`?tab=diff`, none for Comments); switching keeps every other key (the
// board's filters stay). Only the selected tab's panel exists, so the others
// hold no connection and send no request.
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { TicketSummary } from '../../../api/types'
import UiTabs, { type UiTab } from '../../../components/ui/UiTabs.vue'
import { ticketTabs } from './ticketTabs'

const props = defineProps<{ ticket: TicketSummary }>()

const route = useRoute()
const router = useRouter()

const available = computed(() => ticketTabs(props.ticket))
const uiTabs = computed<UiTab[]>(() =>
  available.value.map((t) => {
    const count = t.count?.(props.ticket)
    return count === undefined ? { id: t.id, label: t.label } : { id: t.id, label: t.label, count }
  }),
)

const active = computed(() => {
  const q = route.query.tab
  const want = Array.isArray(q) ? q[0] : q
  return available.value.find((t) => t.id === want)?.id ?? 'comments'
})

const current = computed(() => available.value.find((t) => t.id === active.value))

function select(id: string) {
  const query = { ...route.query }
  delete query.comment
  if (id === 'comments') delete query.tab
  else query.tab = id
  void router.replace({ query })
}
</script>

<template>
  <section class="ptabs" aria-label="Activity">
    <UiTabs :model-value="active" :tabs="uiTabs" :label="`Activity on #${ticket.number}`" @update:model-value="select">
      <template #panel>
        <div class="ptabs__panel">
          <component :is="current.component" v-if="current" :key="current.id" :ticket="ticket" />
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
</style>
