<script setup lang="ts">
// The bell in the top bar: the number of things waiting for the user plus
// unread mentions, and the list of them, opened under the bell (spanning the
// screen on phones). The button's name carries the counts.
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useBoard } from '../../store'
import { openTicketLocation } from '../ticket/openTicket'
import IconButton from '../../components/ui/IconButton.vue'
import NotificationList from './NotificationList.vue'
import { useNotifications, type TicketTarget } from './useNotifications'

const n = useNotifications()
const router = useRouter()
const store = useBoard()
const open = ref(false)
const root = ref<HTMLElement>()
const listId = `bell-${useId()}`

const waiting = computed(() => n.waiting.value.length)
const count = computed(() => waiting.value + n.unread.value)
const label = computed(() => {
  const u = n.unread.value
  return `Notifications: ${waiting.value} waiting for you, ${u} unread mention${u === 1 ? '' : 's'}`
})

function button(): HTMLButtonElement | null {
  return root.value?.querySelector<HTMLButtonElement>('[data-bell]') ?? null
}

function close(focusBell: boolean) {
  open.value = false
  if (focusBell) void nextTick(() => button()?.focus())
}

function go(to: TicketTarget) {
  open.value = false
  void router.push(openTicketLocation(store, to.ticket, router.currentRoute.value, to))
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) {
    e.stopPropagation()
    close(true)
  }
}

// A press anywhere else closes the list, without moving the focus.
function onPointerDown(e: PointerEvent) {
  if (root.value && e.target instanceof Node && !root.value.contains(e.target)) close(false)
}
watch(open, (o) => {
  if (o) document.addEventListener('pointerdown', onPointerDown, true)
  else document.removeEventListener('pointerdown', onPointerDown, true)
})
onBeforeUnmount(() => document.removeEventListener('pointerdown', onPointerDown, true))
</script>

<template>
  <div ref="root" class="bell" @keydown="onKeydown">
    <IconButton :label="label" :aria-expanded="open ? 'true' : 'false'" :aria-controls="listId" data-bell @click="open = !open">
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
        <path d="M3.5 11.5V7a4.5 4.5 0 0 1 9 0v4.5l1 1.5h-11zM6.5 14.2h3" />
      </svg>
    </IconButton>
    <span v-if="count > 0" class="bell__count" aria-hidden="true" data-bell-count>{{ count > 99 ? '99+' : count }}</span>
    <NotificationList v-if="open" :id="listId" @close="close(true)" @go="go" />
  </div>
</template>

<style scoped>
.bell {
  position: relative;
}
.bell__count {
  position: absolute;
  top: calc(var(--omo-space) * -1.5);
  right: calc(var(--omo-space) * -1.5);
  min-width: calc(var(--omo-space) * 4);
  padding: 0 var(--omo-space);
  background: var(--omo-warn);
  color: var(--omo-bg);
  font-size: var(--omo-text-xs);
  font-weight: 700;
  line-height: var(--omo-text-xs-lh);
  text-align: center;
  pointer-events: none;
}
</style>
