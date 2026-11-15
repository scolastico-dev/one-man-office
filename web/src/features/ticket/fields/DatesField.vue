<script setup lang="ts">
// Created, updated and closed in the local time zone, with the exact time as
// a tooltip, and the version of the description.
import { computed } from 'vue'
import type { TicketSummary } from '../../../api/types'
import FieldRow from '../FieldRow.vue'

const props = defineProps<{ ticket: TicketSummary }>()

const short = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const exact = new Intl.DateTimeFormat(undefined, { dateStyle: 'full', timeStyle: 'long' })

const rows = computed(() => {
  const t = props.ticket
  const out = [
    { key: 'created', label: 'Created', at: t.created_at },
    { key: 'updated', label: 'Updated', at: t.updated_at },
  ]
  if (t.closed_at) out.push({ key: 'closed', label: 'Closed', at: t.closed_at })
  return out.map((r) => {
    const d = new Date(r.at)
    const valid = !Number.isNaN(d.getTime())
    return { ...r, text: valid ? short.format(d) : r.at, title: valid ? `${exact.format(d)} (${r.at})` : r.at }
  })
})
</script>

<template>
  <FieldRow v-for="r in rows" :key="r.key" :field="r.key" :label="r.label" :editable="false" :editing="false">
    <template #value>
      <time :datetime="r.at" :title="r.title" tabindex="0">{{ r.text }}</time>
    </template>
  </FieldRow>
  <FieldRow field="description_version" label="Description" :editable="false" :editing="false">
    <template #value>Version {{ ticket.description_version }}</template>
  </FieldRow>
</template>
