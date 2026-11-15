<script setup lang="ts">
// One comment of the thread. The body is Markdown rendered by the one
// renderer (raw HTML off, links checked); who wrote it comes through
// `actorLabel`, which makes names safe to read. Mentions are highlighted from
// the roles the server found in the comment, never from the text alone.
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { Comment } from '../../api/types'
import RelativeTime from '../../components/ui/RelativeTime.vue'
import StatusBadge from '../../components/ui/StatusBadge.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useBoard } from '../../store'
import { ROLE_LABELS } from '../board/model'
import { renderMarkdown } from '../ticket/markdown'
import { mentionNames, type MentionContext } from './mentions'
import { actorLabel, isQuestion } from './thread'

const props = defineProps<{
  comment: Comment
  context: MentionContext
  answered: boolean
  answeredBy: number | null
  /** Written here and not in the log yet. */
  sending?: boolean
  /** Outlined for a moment: the comment a link pointed at. */
  flash?: boolean
}>()
const emit = defineEmits<{ answer: [comment: Comment]; goto: [id: number] }>()

const store = useBoard()
const route = useRoute()
const router = useRouter()

const author = computed(() => {
  const l = actorLabel(props.comment.author)
  return l.charAt(0).toUpperCase() + l.slice(1)
})
const system = computed(() => props.comment.author.role === 'system')
const question = computed(() => isQuestion(props.comment))
const forYou = computed(() => props.comment.question_to === 'user')
const addressee = computed(() => (forYou.value ? 'you' : (ROLE_LABELS[props.comment.question_to] ?? props.comment.question_to)))
const ticket = computed(() => store.view(props.comment.ticket))

const html = computed(() =>
  renderMarkdown(
    props.comment.body,
    (n) => {
      const t = store.view(n)
      return t === undefined ? undefined : { title: t.title, column: t.column }
    },
    { mentions: mentionNames(props.comment.mentions, ticket.value, props.context) },
  ),
)

function onClick(e: MouseEvent) {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const a = (e.target as Element | null)?.closest?.('a[data-ticket-ref]')
  if (!a) return
  e.preventDefault()
  const query = { ...route.query }
  delete query.tab
  delete query.comment
  void router.push({ path: `/${a.getAttribute('data-ticket-ref')}`, query })
}
</script>

<template>
  <article
    :id="`comment-${comment.id}`"
    class="cmt"
    :class="{ 'cmt--question': question && !answered, 'cmt--you': forYou && !answered, 'cmt--system': system, 'cmt--flash': flash }"
    tabindex="-1"
    :aria-label="`Comment by ${author}`"
    :data-comment="comment.id"
    :data-question="question ? (answered ? 'answered' : 'open') : undefined"
  >
    <header class="cmt__head">
      <span class="cmt__author">{{ author }}</span>
      <RelativeTime v-if="!sending" class="cmt__time" :at="comment.created_at" />
      <span v-else class="cmt__time">sending…</span>
      <StatusBadge v-if="question && !answered && forYou" tone="warn" text="Question for you" />
      <StatusBadge v-else-if="question" :tone="answered ? 'ok' : 'muted'" :text="`Question to ${addressee}`" />
      <StatusBadge v-if="question" :tone="answered ? 'ok' : 'muted'" :text="answered ? 'Answered' : 'Waiting for answer'" />
    </header>
    <p v-if="comment.reply_to !== null" class="cmt__reply">
      <button type="button" class="cmt__link" @click="emit('goto', comment.reply_to)">In reply to comment {{ comment.reply_to }}</button>
    </p>
    <!-- renderMarkdown: raw HTML off, links checked, images never loaded, mentions from the server's list. -->
    <div class="cmt__body" data-comment-body v-html="html" @click="onClick" />
    <div v-if="question && (!answered || answeredBy !== null)" class="cmt__actions">
      <UiButton v-if="!answered" size="sm" variant="primary" data-answer @click="emit('answer', comment)">Answer</UiButton>
      <button v-else-if="answeredBy !== null" type="button" class="cmt__link" @click="emit('goto', answeredBy)">Show the answer</button>
    </div>
  </article>
</template>

<style scoped>
.cmt {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
}
.cmt:focus-visible,
.cmt--flash {
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: var(--omo-border);
}
.cmt--system {
  color: var(--omo-muted);
}
.cmt--question {
  border-color: var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.cmt__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--omo-space) calc(var(--omo-space) * 2);
}
.cmt__author {
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.cmt__time {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.cmt__reply {
  margin: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.cmt__link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--omo-accent);
  font: inherit;
  text-decoration: underline;
  cursor: pointer;
}
.cmt__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-xs);
}
.cmt__body {
  min-width: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.cmt__body :deep(> :first-child) {
  margin-top: 0;
}
.cmt__body :deep(> :last-child) {
  margin-bottom: 0;
}
.cmt__body :deep(p),
.cmt__body :deep(ul),
.cmt__body :deep(ol),
.cmt__body :deep(blockquote),
.cmt__body :deep(pre) {
  margin: 0 0 calc(var(--omo-space) * 2);
}
.cmt__body :deep(code) {
  padding: 0 var(--omo-space);
  background: var(--omo-raised);
}
.cmt__body :deep(pre) {
  /* Wraps instead of scrolling, so no keyboard-unreachable scroll box. */
  white-space: pre-wrap;
  padding: calc(var(--omo-space) * 2);
  background: var(--omo-raised);
}
.cmt__body :deep(pre code) {
  padding: 0;
}
.cmt__body :deep(a) {
  color: var(--omo-accent);
}
.cmt__body :deep(.mention) {
  padding: 0 calc(var(--omo-space) * 0.5);
  background: var(--omo-mention);
}
.cmt__body :deep(.mention--you) {
  font-weight: 700;
}
</style>
