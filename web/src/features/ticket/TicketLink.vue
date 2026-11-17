<script setup lang="ts">
// A link to a ticket. Its address is the ticket's own page, so opening it in
// a new tab or window (middle click, Ctrl or Cmd click, the context menu)
// shows the page; a plain click stays in the view the user is in: the panel
// over the board, or from a ticket's page that ticket's page.
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ticketPageLocation, type TicketKeys } from '../../locations'
import { useBoard } from '../../store'
import { openTicketLocation } from './openTicket'

const props = defineProps<{
  number: number
  tab?: string
  comment?: number
}>()

const route = useRoute()
const router = useRouter()
const store = useBoard()

const keys = computed<TicketKeys>(() => ({
  ...(props.tab !== undefined ? { tab: props.tab } : {}),
  ...(props.comment !== undefined ? { comment: props.comment } : {}),
}))
const href = computed(() => router.resolve(ticketPageLocation(props.number, route, keys.value)).href)

function onClick(e: MouseEvent) {
  // New tabs and windows are the browser's.
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.defaultPrevented) return
  e.preventDefault()
  void router.push(openTicketLocation(store, props.number, route, keys.value))
}
</script>

<template>
  <a :href="href" data-ticket-link @click="onClick"><slot /></a>
</template>
