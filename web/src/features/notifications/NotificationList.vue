<script setup lang="ts">
// The bell's list: what waits for the user, the comments that mention the
// user, and the footer with the desktop switch and "Mark all as read". Every
// text here may come from an agent and is shown through text interpolation
// only, made visible and single-line first.
import { computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ticketLocation } from '../../locations'
import RelativeTime from '../../components/ui/RelativeTime.vue'
import StatusBadge from '../../components/ui/StatusBadge.vue'
import Toggle from '../../components/ui/Toggle.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import type { MentionNotice } from '../../api/types'
import { authorOf, commentTarget, snippetOf, useNotifications, type TicketTarget, type WaitingItem, type WaitingKind } from './useNotifications'

defineProps<{ id: string }>()
const emit = defineEmits<{ close: []; go: [to: TicketTarget] }>()

const n = useNotifications()
const router = useRouter()
const announcer = useAnnouncer()

const TONES: Record<WaitingKind, 'warn' | 'accent' | 'danger'> = { question: 'warn', review: 'accent', attention: 'danger' }

const mentions = computed(() =>
  n.mentions.value.map((m: MentionNotice) => ({
    id: m.comment.id,
    ticket: m.comment.ticket,
    by: authorOf(m.comment.author),
    text: snippetOf(m.comment),
    at: m.comment.created_at,
    unread: n.isUnread(m),
    to: commentTarget(m.comment.ticket, m.comment.id),
  })),
)

function href(to: TicketTarget): string {
  return router.resolve(ticketLocation(to.ticket, router.currentRoute.value, to)).href
}

function waitingTarget(w: WaitingItem): TicketTarget {
  return w.kind === 'question' ? { ticket: w.ticket, tab: 'comments' } : { ticket: w.ticket }
}

async function openWaiting(w: WaitingItem) {
  emit('go', await n.targetOf(w))
}

function openMention(id: number, to: TicketTarget) {
  void n.markRead(id)
  emit('go', to)
}

const desktopOn = computed({
  get: () => n.desktop.enabled.value,
  set: (v: boolean) => {
    if (v) void n.desktop.enable()
    else n.desktop.disable()
  },
})

// Refusals are shown in place and read out.
watch(
  () => n.desktop.reason.value,
  (r) => {
    if (r !== '') announcer.polite(r)
  },
)
watch(
  () => n.error.value,
  (e) => {
    if (e !== '') announcer.polite(e)
  },
)

function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  e.stopPropagation()
  emit('close')
}
</script>

<template>
  <div :id="id" class="bell-list" role="region" aria-label="Notifications" data-bell-list @keydown="onKeydown">
    <section class="bell-list__group" :aria-labelledby="`${id}-waiting`">
      <h2 :id="`${id}-waiting`" class="bell-list__h">
        Waiting for you <span class="bell-list__n">{{ n.waiting.value.length }}</span>
      </h2>
      <ul v-if="n.waiting.value.length > 0" class="bell-list__items">
        <li v-for="w in n.waiting.value" :key="w.ticket">
          <a class="bell-list__item" :href="href(waitingTarget(w))" :data-waiting="w.ticket" @click.prevent="openWaiting(w)">
            <span class="bell-list__title"><span class="bell-list__num">#{{ w.ticket }}</span> {{ w.title }}</span>
            <StatusBadge :tone="TONES[w.kind]" :text="w.text" />
          </a>
        </li>
      </ul>
      <p v-else class="bell-list__empty">Nothing waits for you.</p>
    </section>
    <section class="bell-list__group" :aria-labelledby="`${id}-mentions`">
      <h2 :id="`${id}-mentions`" class="bell-list__h">
        Mentions <span class="bell-list__n">{{ n.unread.value }} unread</span>
      </h2>
      <ul v-if="mentions.length > 0" class="bell-list__items">
        <li v-for="m in mentions" :key="m.id">
          <a
            class="bell-list__item"
            :class="{ 'bell-list__item--unread': m.unread }"
            :href="href(m.to)"
            data-mention
            :data-unread="m.unread ? 'true' : 'false'"
            @click.prevent="openMention(m.id, m.to)"
          >
            <span class="bell-list__meta"
              ><span v-if="m.unread" class="bell-list__dot" aria-hidden="true"></span><span v-if="m.unread" class="sr-only">Unread: </span
              ><span class="bell-list__num">#{{ m.ticket }}</span> {{ m.by }} · <RelativeTime :at="m.at"
            /></span>
            <span class="bell-list__text">{{ m.text }}</span>
          </a>
        </li>
      </ul>
      <p v-else class="bell-list__empty">No mentions yet.</p>
    </section>
    <p v-if="n.error.value" class="bell-list__problem" data-bell-error>{{ n.error.value }}</p>
    <div v-if="n.desktop.supported || n.unread.value > 0" class="bell-list__foot">
      <div v-if="n.desktop.supported" class="bell-list__desktop" data-desktop>
        <Toggle v-model="desktopOn" label="Desktop notifications" />
        <p v-if="n.desktop.reason.value" class="bell-list__problem" data-desktop-reason>{{ n.desktop.reason.value }}</p>
      </div>
      <button v-if="n.unread.value > 0" type="button" class="bell-list__mark" data-mark-all @click="n.markAllRead()">Mark all as read</button>
    </div>
  </div>
</template>

<style scoped>
.bell-list {
  position: absolute;
  z-index: 30;
  top: calc(100% + var(--omo-space) * 1.5);
  right: 0;
  display: grid;
  gap: calc(var(--omo-space) * 3);
  width: 380px;
  max-width: calc(100vw - var(--omo-space) * 6);
  max-height: calc(100dvh - var(--omo-space) * 20);
  overflow-y: auto;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  box-shadow: var(--omo-space) var(--omo-space) 0 var(--omo-backdrop);
}
/* Phones: the list spans the screen under the top bar instead of hanging from the bell. */
@media (max-width: 767.98px) {
  .bell-list {
    position: fixed;
    top: calc(var(--omo-space) * 14);
    right: calc(var(--omo-space) * 3);
    left: calc(var(--omo-space) * 3);
    width: auto;
    max-width: none;
  }
}
.bell-list__group {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
}
.bell-list__h {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.bell-list__n {
  color: var(--omo-accent);
  letter-spacing: 0;
  text-transform: none;
}
.bell-list__items {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}
.bell-list__item {
  display: grid;
  justify-items: start;
  gap: var(--omo-space);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) solid transparent;
  border-left: var(--omo-edge) solid transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-decoration: none;
}
.bell-list__item:focus-visible {
  border-color: var(--omo-select-line);
  background: var(--omo-select);
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.bell-list__item--unread {
  border-left-color: var(--omo-accent);
}
@media (hover: hover) {
  .bell-list__item:hover {
    background: var(--omo-accent-soft);
  }
}
.bell-list__title,
.bell-list__text {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bell-list__num {
  color: var(--omo-muted);
}
.bell-list__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 var(--omo-space);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.bell-list__item--unread .bell-list__text {
  font-weight: 700;
}
.bell-list__dot {
  width: calc(var(--omo-space) * 1.5);
  height: calc(var(--omo-space) * 1.5);
  margin-right: var(--omo-space);
  background: var(--omo-accent);
}
.bell-list__empty {
  margin: 0;
  padding: 0 calc(var(--omo-space) * 2.5);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.bell-list__problem {
  margin: 0;
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
/* A text button: the top bar shrinks its buttons to icons on phones, and this one has no icon. */
.bell-list__mark {
  min-height: calc(var(--omo-space) * 7);
  padding: 0 var(--omo-space);
  border: 0;
  background: transparent;
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  cursor: pointer;
}
@media (hover: hover) {
  .bell-list__mark:hover {
    text-decoration: underline;
  }
}
.bell-list__desktop {
  display: grid;
  gap: var(--omo-space);
}
.bell-list__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  padding-top: calc(var(--omo-space) * 3);
  border-top: var(--omo-border) solid var(--omo-line);
}
</style>
