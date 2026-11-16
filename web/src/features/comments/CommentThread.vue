<script setup lang="ts">
// The Comments tab: the ticket's thread, newest at the bottom, with "Load
// older" above it and the composer below. New entries are announced politely;
// the page does not scroll by itself when entries arrive or older ones load.
// `?comment=<id>` scrolls to that comment, focuses it and outlines it for 2 s.
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { Comment, CommentRequest, TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { revealInBox } from '../../components/ui/scrollBox'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import CommentComposer from './CommentComposer.vue'
import CommentItem from './CommentItem.vue'
import type { MentionContext } from './mentions'
import StatusLine from './StatusLine.vue'
import { actorLabel } from './thread'
import { useThread } from './useThread'
import { announcedByBell } from '../notifications/useNotifications'
import { useBoard } from '../../store'
import { uploadImage } from '../../api/endpoints'
import { useSession } from '../../session'

const props = defineProps<{ ticket: TicketSummary }>()

const route = useRoute()
const store = useBoard()
const announcer = useAnnouncer()
const thread = useThread(props.ticket.number)
const session = useSession()
const upload = async (file: Blob, name: string) => (await uploadImage(session.client, props.ticket.number, file, name)).data
const { items } = thread

const context = computed<MentionContext>(() => ({ ceoEnabled: store.state.general?.ceo.enabled ?? false, agents: props.ticket.agents }))

const answering = ref<Comment | null>(null)
const composer = ref<InstanceType<typeof CommentComposer> | null>(null)
const list = ref<HTMLElement | null>(null)

function startAnswering(c: Comment) {
  answering.value = c
  void nextTick(() => composer.value?.focus())
}
// A question that got its answer from elsewhere stops being the one to answer.
watch(items, (all) => {
  const q = answering.value
  if (!q) return
  const it = all.find((x) => x.kind === 'comment' && x.comment.id === q.id)
  if (it && it.kind === 'comment' && it.answered) answering.value = null
})

async function send(req: CommentRequest) {
  return thread.post(req)
}
function sent() {
  answering.value = null
}

// Scrolling

/** The nearest ancestor that scrolls. */
function scroller(from: HTMLElement | null): HTMLElement | null {
  for (let el = from?.parentElement ?? null; el; el = el.parentElement) {
    const y = getComputedStyle(el).overflowY
    if (y === 'auto' || y === 'scroll') return el
  }
  return null
}

async function loadOlder() {
  const el = scroller(list.value)
  const height = el?.scrollHeight ?? 0
  const top = el?.scrollTop ?? 0
  await thread.loadOlder()
  await nextTick()
  // What the user was reading stays where it is.
  if (el) el.scrollTop = top + (el.scrollHeight - height)
}

// New entries

const known = new Set<number>()
let primed = false
watch(
  [items, () => thread.state.value],
  ([all]) => {
    const fresh = all.filter((it) => !known.has(it.eventId === Number.MAX_SAFE_INTEGER && it.kind === 'comment' ? -it.comment.id : it.eventId))
    for (const it of all) known.add(it.eventId === Number.MAX_SAFE_INTEGER && it.kind === 'comment' ? -it.comment.id : it.eventId)
    if (!primed) {
      if (thread.state.value === 'ready') primed = true
      return
    }
    // The bell announces mentions of the user and questions to the user, with more detail.
    const theirs = fresh.filter((it) => !(it.kind === 'comment' && (it.comment.author.role === 'user' || announcedByBell(it.comment))))
    if (theirs.length === 0) return
    if (theirs.length > 3) announcer.polite(`${theirs.length} new entries in the thread.`)
    else
      for (const it of theirs)
        announcer.polite(it.kind === 'comment' ? `New comment from ${actorLabel(it.comment.author)}.` : it.text)
  },
  { flush: 'post' },
)

// ?comment=<id>

const flashed = ref<number | null>(null)
let flashTimer: ReturnType<typeof setTimeout> | undefined
let wanted: string | null = null

/**
 * Whether the comment can still be on an older page. Comment ids grow over
 * the whole board and the pages are read without gaps, so once a comment
 * older than it is shown and it is not, it belongs to another ticket.
 */
function mayBeOlder(id: number): boolean {
  return !items.value.some((it) => it.kind === 'comment' && !it.sending && it.comment.id < id)
}

async function reveal(id: number) {
  let el = document.getElementById(`comment-${id}`)
  for (let i = 0; !el && thread.more.value && mayBeOlder(id) && i < 20; i++) {
    await loadOlder()
    // The reason shows above the thread; asking again is the user's choice.
    if (thread.olderError.value !== '') return
    el = document.getElementById(`comment-${id}`)
  }
  // Not in this thread: the thread stays as it is, without a word.
  if (!el) return
  revealInBox(el, 'center')
  el.focus({ preventScroll: true })
  flashed.value = id
  clearTimeout(flashTimer)
  flashTimer = setTimeout(() => (flashed.value = null), 2000)
}

function wantedId(): number | null {
  const q = route.query.comment
  const v = Array.isArray(q) ? q[0] : q
  return typeof v === 'string' && /^[1-9]\d{0,9}$/.test(v) ? Number(v) : null
}

watch(
  [() => thread.state.value, () => route.query.comment],
  async ([state]) => {
    const id = wantedId()
    if (state !== 'ready' || id === null) return
    const key = `${id}`
    if (wanted === key) return
    wanted = key
    await nextTick()
    await reveal(id)
  },
  { immediate: true },
)

onBeforeUnmount(() => clearTimeout(flashTimer))
</script>

<template>
  <div class="thread" data-thread>
    <p v-if="thread.state.value === 'loading'" class="thread__note" aria-live="polite">Loading the comments…</p>
    <div v-else-if="thread.state.value === 'failed'" class="thread__note" role="alert">
      <p class="thread__text">{{ thread.error.value }}</p>
      <UiButton size="sm" @click="thread.load()">Try again</UiButton>
    </div>
    <template v-else>
      <div v-if="thread.more.value || thread.olderError.value" class="thread__older">
        <UiButton size="sm" :disabled="thread.loadingOlder.value" data-load-older @click="loadOlder">
          {{ thread.loadingOlder.value ? 'Loading…' : 'Load older' }}
        </UiButton>
        <p v-if="thread.olderError.value" class="thread__error" role="alert">{{ thread.olderError.value }}</p>
      </div>
      <p v-if="items.length === 0" class="thread__note" data-thread-empty>No comments yet.</p>
      <ol ref="list" class="thread__list" aria-label="Comments and events">
        <li v-for="it in items" :key="it.kind === 'comment' ? `c${it.comment.id}` : `e${it.eventId}`">
          <StatusLine v-if="it.kind === 'status'" :id="it.eventId" :text="it.text" :at="it.at" />
          <CommentItem
            v-else
            :comment="it.comment"
            :context="context"
            :answered="it.answered"
            :answered-by="it.answeredBy"
            :sending="it.sending"
            :flash="flashed === it.comment.id"
            @answer="startAnswering"
            @goto="reveal"
          />
        </li>
      </ol>
    </template>
    <CommentComposer
      ref="composer"
      :ticket="ticket"
      :context="context"
      :send="send"
      :upload="upload"
      :answering="answering"
      @stop-answering="answering = null"
      @sent="sent"
    />
  </div>
</template>

<style scoped>
.thread {
  min-width: 0;
}
.thread__list {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0;
  list-style: none;
  overflow-anchor: none;
}
.thread__older {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
  margin-bottom: calc(var(--omo-space) * 3);
}
.thread__note,
.thread__text,
.thread__error {
  margin: 0 0 calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.thread__error {
  color: var(--omo-danger);
}
</style>
