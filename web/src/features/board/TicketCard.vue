<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../../components/ui/Chip.vue'
import LiveDot from '../../components/ui/LiveDot.vue'
import StatusBadge from '../../components/ui/StatusBadge.vue'
import TypeIcon from '../../components/ui/TypeIcon.vue'
import MoveMenu from './MoveMenu.vue'
import { ROLE_LABELS, shortenMiddle, type CardModel } from './model'
import { useMoves } from './useMoves'

const props = withDefaults(
  defineProps<{
    card: CardModel
    dragging?: boolean
    /** A dragged card would land right above or below this one. */
    dropMark?: 'before' | 'after' | null
    /** The copy that follows the pointer during a drag; it is not the ticket's card. */
    ghost?: boolean
  }>(),
  { dragging: false, dropMark: null, ghost: false },
)
const emit = defineEmits<{ open: [n: number]; 'filter-story': [n: number] }>()
/** The board's moves; absent on the sample board, which then shows no move menu. */
const moves = useMoves()

/** Longest project name shown before it is shortened in the middle. */
const PROJECT_CHARS = 24

const t = computed(() => props.card.ticket)

const project = computed(() => {
  const [first, ...rest] = props.card.projectNames
  if (first === undefined) return null
  return { text: shortenMiddle(first, PROJECT_CHARS), more: rest.length, title: props.card.projectNames.join(', ') }
})

const storyLabel = computed(() => {
  const n = t.value.story
  if (t.value.type !== 'task' || n === null) return null
  return props.card.storyTitle ? `#${n} ${props.card.storyTitle}` : `#${n}`
})

/** The agent working on the ticket now; a story's product manager, a task's developer or reviewer. */
const agent = computed(() => {
  const live = t.value.agents.filter((a) => a.state !== 'ended')
  const first = live[0]
  if (!first) return null
  return {
    state: first.state as 'running' | 'starting' | 'waiting',
    role: ROLE_LABELS[first.role],
    name: first.name,
    word: first.state === 'waiting' ? 'parked' : first.state === 'starting' ? 'starting' : '',
    more: live.length - 1,
  }
})

const progress = computed(() => {
  const p = t.value.progress
  if (t.value.type !== 'story' || !p) return null
  return {
    ...p,
    text: p.total === 0 ? 'no tasks yet' : `${p.done} of ${p.total} tasks done`,
    percent: p.total === 0 ? 0 : Math.round((p.done / p.total) * 100),
  }
})

const busy = computed(() => t.value.busy === true)

function onOpen(event: MouseEvent) {
  // Let the browser handle new-tab and new-window clicks on the link.
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  event.preventDefault()
  emit('open', t.value.number)
}
</script>

<template>
  <li
    class="card"
    :class="{
      'card--story': t.type === 'story',
      [`card--${card.badge?.tone}`]: card.badge,
      'card--busy': busy,
      'card--pending': card.pending,
      'card--refused': card.refusedFlash,
      'card--dragging': dragging,
      'card--closed': t.column === 'done',
      [`card--drop-${dropMark}`]: dropMark,
    }"
    :data-ticket="ghost ? undefined : t.number"
  >
    <div class="card__meta">
      <TypeIcon :type="t.type" />
      <span class="card__number">#{{ t.number }}</span>
    </div>

    <a class="card__link" :href="`/${t.number}`" :title="t.title" draggable="false" data-card-link @click="onOpen">{{ t.title }}</a>

    <div v-if="project || storyLabel" class="card__chips">
      <span v-if="project" class="card__chip" data-chip="project" :title="project.title">
        <Chip :label="project.text" /> <span v-if="project.more" class="card__more">+{{ project.more }}</span>
      </span>
      <span v-if="storyLabel" class="card__chip card__chip--story">
        <Chip
          as="button"
          data-chip="story"
          :label="storyLabel"
          :title="`Show only story ${storyLabel}`"
          @click="emit('filter-story', t.story!)"
        />
      </span>
    </div>

    <p v-if="agent" class="card__agent" data-agent>
      <LiveDot :state="agent.state" />
      <span class="card__who"
        ><span class="card__role">{{ ` ${agent.role}` }}</span
        ><span class="card__name">{{ ` · ${agent.name}` }}</span></span
      >
      <span v-if="agent.word" class="card__agent-word" aria-hidden="true">{{ agent.word }}</span>
      <span v-if="agent.more" class="card__more">+{{ agent.more }}</span>
    </p>

    <div v-if="card.badge || card.caption" class="card__status">
      <span v-if="card.badge" class="card__badge" data-badge>
        <StatusBadge :tone="card.badge.tone" :text="card.badge.text" :title="card.badge.title">
          <template #icon>
            <svg width="12" height="12" viewBox="0 0 12 12" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
              <template v-if="card.badge.tone === 'danger'">
                <path d="M6 1.2 11.2 10.6H.8z" stroke-linejoin="miter" />
                <path d="M6 4.6v3" />
                <path d="M6 8.6v1" />
              </template>
              <template v-else-if="card.badge.tone === 'warn'">
                <circle cx="6" cy="6" r="5.1" />
                <path d="M4.4 4.7a1.65 1.65 0 1 1 2.3 1.5c-.5.25-.7.6-.7 1.1v.4" />
                <path d="M6 8.6v.9" />
              </template>
              <template v-else-if="card.badge.tone === 'accent'">
                <path d="M.8 6c1.6-3.1 8.8-3.1 10.4 0-1.6 3.1-8.8 3.1-10.4 0z" />
                <circle cx="6" cy="6" r="1.6" fill="currentColor" stroke="none" />
              </template>
              <template v-else>
                <path d="M2.5 1h7M2.5 11h7M3.5 1c0 3 5 2.6 5 5s-5 2-5 5M8.5 1c0 3-5 2.6-5 5s5 2 5 5" />
              </template>
            </svg>
          </template>
        </StatusBadge>
      </span>
      <span v-if="card.caption" class="card__caption">
        <svg v-if="busy" class="card__busy-icon" width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
          <path d="M10.2 6.6A4.2 4.2 0 1 1 9 2.9" />
          <path d="M9.6 .8v2.6H7" />
        </svg>
        {{ card.caption }}
      </span>
    </div>
    <p v-if="card.badge?.tone === 'danger' && card.badge.title" class="card__reason">{{ card.badge.title }}</p>
    <p v-if="card.notice" class="card__notice" :class="`card__notice--${card.notice.tone}`" data-move-notice>{{ card.notice.text }}</p>

    <div v-if="progress" class="card__progress">
      <span class="card__progress-text">{{ progress.text }}</span>
      <span
        v-if="progress.total > 0"
        class="card__bar"
        role="progressbar"
        aria-label="Tasks done"
        :aria-valuenow="progress.done"
        aria-valuemin="0"
        :aria-valuemax="progress.total"
      >
        <span class="card__bar-fill" :style="{ width: `${progress.percent}%` }" />
      </span>
    </div>

    <MoveMenu v-if="moves && !ghost" class="card__move" :ticket="t" :moves="moves" @move="(target, before) => moves!.drop(t.number, target, before)" />
  </li>
</template>

<style scoped>
.card {
  position: relative;
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
  transition:
    border-color var(--omo-duration) ease,
    opacity var(--omo-duration) ease;
}

/* A story: accent edge on the left, so a pinned story never reads as a task. */
.card--story {
  padding-left: calc(var(--omo-space) * 2.5 - var(--omo-edge) + var(--omo-border));
  border-left: var(--omo-edge) solid var(--omo-accent);
}

/* States that need the user get a coloured frame on top of their worded badge. */
.card--danger {
  border-color: var(--omo-danger);
}
.card--warn {
  border-color: var(--omo-warn);
}
.card--accent {
  border-color: var(--omo-select-line);
  background: var(--omo-select);
}
.card--story.card--danger,
.card--story.card--warn,
.card--story.card--accent {
  border-left-color: var(--omo-accent);
}

/* omo is starting or merging it: a dashed frame, like work in motion. */
.card--busy {
  border-style: dashed;
  border-color: var(--omo-line-strong);
}
.card--story.card--busy {
  border-left-style: solid;
}

.card--pending {
  opacity: 0.7;
}
.card--refused {
  outline: var(--omo-focus-width) solid var(--omo-danger);
  outline-offset: calc(var(--omo-border) * -1);
}
.card--dragging {
  border-style: dashed;
  border-color: var(--omo-accent);
  background: transparent;
}
.card--dragging > * {
  opacity: 0.4;
}

/* Where a dragged card would land: a line in the gap above or below this card. */
.card--drop-before::before,
.card--drop-after::before {
  content: '';
  position: absolute;
  right: calc(var(--omo-border) * -1);
  left: calc(var(--omo-border) * -1);
  height: calc(var(--omo-border) * 2);
  background: var(--omo-accent);
  pointer-events: none;
}
.card--drop-before::before {
  top: calc(var(--omo-space) * -1 - var(--omo-border) * 2);
}
.card--drop-after::before {
  bottom: calc(var(--omo-space) * -1 - var(--omo-border) * 2);
}

/* A long press picks a card up on touch screens; it must not select text or open a callout. */
@media (hover: none) {
  .card {
    -webkit-touch-callout: none;
    -webkit-user-select: none;
    user-select: none;
  }
}

/* The title link covers the whole card; chips sit above it. */
.card:has(.card__link:focus-visible) {
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: var(--omo-focus-offset);
}
@media (hover: hover) {
  .card:hover:not(.card--dragging) {
    border-color: var(--omo-accent);
  }
  .card--story:hover {
    border-left-color: var(--omo-accent);
  }
}

/* The "Move…" menu: in the top right corner, shown on hover and focus where
   there is a mouse, always on touch screens and narrow screens. */
.card__move {
  position: absolute;
  z-index: 2;
  top: var(--omo-space);
  right: var(--omo-space);
}
.card__move:has([aria-expanded='true']) {
  z-index: 20;
}
@media (hover: hover) and (min-width: 768px) {
  .card__move {
    opacity: 0;
  }
  .card:hover .card__move,
  .card:focus-within .card__move,
  .card__move:has([aria-expanded='true']) {
    opacity: 1;
  }
}
@media (hover: none), (max-width: 767.98px) {
  .card:has(.card__move) {
    padding-right: calc(var(--omo-space) * 13);
  }
  .card__move :deep(.omo-btn) {
    width: calc(var(--omo-space) * 11);
    min-height: calc(var(--omo-space) * 11);
  }
}

.card__meta {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  font-variant-numeric: tabular-nums;
}

.card__link {
  display: -webkit-box;
  overflow: hidden;
  color: var(--omo-fg);
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
  text-decoration: none;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
}
.card__link:focus-visible {
  outline: none;
}
.card__link::after {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 1;
}
.card--closed .card__link {
  color: var(--omo-muted);
}

.card__chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--omo-space);
  min-width: 0;
}
.card__chip {
  position: relative;
  z-index: 2;
  display: inline-flex;
  align-items: center;
  gap: var(--omo-space);
  max-width: 100%;
  min-width: 0;
}
.card__chip--story {
  flex: 1 1 auto;
}
.card__chip :deep(.chip) {
  min-width: 0;
}
.card__more {
  flex-shrink: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}

.card__agent {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  margin: 0;
  color: var(--omo-fg);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.card__agent {
  gap: var(--omo-space);
}
.card__agent > :first-child {
  margin-right: calc(var(--omo-space) * 0.5);
}
.card__who {
  display: flex;
  min-width: 0;
}
/* The role gives way first; the agent's name is what tells agents apart. */
.card__role {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.card__name {
  flex-shrink: 0;
  white-space: pre;
}
.card__agent-word {
  margin-left: calc(var(--omo-space) * 0.5);
}
.card__agent-word {
  flex-shrink: 0;
  color: var(--omo-muted);
}

.card__status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--omo-space) calc(var(--omo-space) * 2);
  min-width: 0;
}
.card__badge {
  display: inline-flex;
  max-width: 100%;
  min-width: 0;
}
.card__caption {
  display: inline-flex;
  align-items: center;
  gap: var(--omo-space);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-style: italic;
  line-height: var(--omo-text-xs-lh);
}
.card--busy .card__caption {
  color: var(--omo-fg);
}
@media (prefers-reduced-motion: no-preference) {
  .card__busy-icon {
    animation: card-spin 1.6s linear infinite;
  }
}
@keyframes card-spin {
  to {
    transform: rotate(360deg);
  }
}
.card__reason {
  margin: 0;
  overflow: hidden;
  color: var(--omo-fg);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card__notice {
  margin: 0;
  padding-left: calc(var(--omo-space) * 2);
  border-left: calc(var(--omo-border) * 2) solid var(--omo-line-strong);
  color: var(--omo-fg);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.card__notice--danger {
  border-left-color: var(--omo-danger);
}

.card__progress {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.card__progress-text {
  flex-shrink: 0;
}
.card__bar {
  flex: 1 1 auto;
  height: var(--omo-space);
  min-width: calc(var(--omo-space) * 6);
  background: var(--omo-line);
}
.card__bar-fill {
  display: block;
  height: 100%;
  background: var(--omo-accent);
}
</style>
