<script setup lang="ts">
// The ticket's head: type, number, title (editable in place: Enter saves,
// Escape cancels), column, how it ended, its badge and state, and for an
// exported ticket the addresses of its pull requests.
import { computed, nextTick, ref, shallowRef, watch } from 'vue'
import { updateTicket } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { TicketSummary } from '../../api/types'
import { badgeFor, captionFor } from '../../board/badges'
import IconButton from '../../components/ui/IconButton.vue'
import StatusBadge from '../../components/ui/StatusBadge.vue'
import TypeIcon from '../../components/ui/TypeIcon.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { hasHidden } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { COLUMN_TITLES } from '../board/model'

const props = withDefaults(
  defineProps<{
    ticket: TicketSummary
    titleId: string
    /** The title's heading level: 2 in the panel, 1 on the ticket's own page. */
    level?: 1 | 2
  }>(),
  { level: 2 },
)
const emit = defineEmits<{ dirty: [dirty: boolean] }>()

/** The server's limit, in characters. */
const TITLE_MAX = 200

const store = useBoard()
const session = useSession()
const say = useAnnouncer()

const editing = ref(false)
const draft = ref('')
/** The title when editing began. */
const base = ref('')
const error = ref('')
const saving = ref(false)
/** A saved title is shown until the stream has delivered its event. */
const awaiting = shallowRef<{ text: string; eventId: number } | null>(null)
const input = ref<HTMLTextAreaElement>()
const editButton = ref<InstanceType<typeof IconButton>>()

const readonly = computed(() => props.ticket.column === 'done')
const shownTitle = computed(() => {
  const a = awaiting.value
  return a !== null && store.state.lastEventId < a.eventId ? a.text : props.ticket.title
})
const changedMeanwhile = computed(() => editing.value && props.ticket.title !== base.value && !saving.value)
const length = computed(() => [...draft.value.trim()].length)
const dirty = computed(() => editing.value && draft.value !== base.value)
watch(dirty, (d) => emit('dirty', d))

const badge = computed(() => badgeFor(props.ticket, store.slots.value))
const caption = computed(() => captionFor(props.ticket))
const resolution = computed(() => {
  switch (props.ticket.resolution) {
    case 'completed':
      return 'completed'
    case 'cancelled':
      return 'cancelled'
    case 'exported':
      return props.ticket.export_merged ? 'exported, merged' : 'exported, not merged yet'
    default:
      return ''
  }
})
// The caption repeats the resolution of a Done ticket.
const stateText = computed(() => (props.ticket.column === 'done' ? '' : caption.value))

/** One pull request address per project, else the ticket's addresses one per line. */
const pullRequests = computed(() => {
  const t = props.ticket
  if (t.resolution !== 'exported' && !t.projects.some((p) => p.export_url)) return []
  const per = t.projects.filter((p) => p.export_url).map((p) => ({ project: p.project, url: p.export_url! }))
  const list = per.length > 0 ? per : (t.export_url ?? '').split('\n').map((url) => ({ project: '', url: url.trim() }))
  return list.filter((x) => /^https?:\/\//i.test(x.url)).map((x) => ({ ...x, name: store.state.projects.get(x.project)?.name ?? x.project }))
})

async function startEdit() {
  if (readonly.value) return
  base.value = props.ticket.title
  draft.value = props.ticket.title
  error.value = ''
  editing.value = true
  await nextTick()
  grow()
  input.value?.focus()
  input.value?.select()
}

/** The title field is as tall as its wrapped lines, so a long title shows in full on a phone. */
function grow() {
  const el = input.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${el.scrollHeight + el.offsetHeight - el.clientHeight}px`
}

function endEdit() {
  editing.value = false
  error.value = ''
  void nextTick(() => (editButton.value?.$el as HTMLElement | undefined)?.focus())
}

function validate(text: string): string {
  if (text === '') return 'Write a title.'
  if ([...text].length > TITLE_MAX) return `The title has ${[...text].length} characters; at most ${TITLE_MAX} fit.`
  if (hasHidden(text)) return 'The title must be one line without control characters.'
  return ''
}

async function save() {
  if (saving.value) return
  const text = draft.value.trim()
  error.value = validate(text)
  if (error.value) return
  if (text === props.ticket.title) {
    endEdit()
    return
  }
  saving.value = true
  try {
    const res = await updateTicket(session.client, props.ticket.number, { title: text })
    if (res.eventId !== null && store.state.lastEventId < res.eventId) awaiting.value = { text, eventId: res.eventId }
    say.polite(`Title of #${props.ticket.number} saved.`)
    endEdit()
  } catch (e) {
    error.value = failureText(e, {
      lost: `No answer from omo, so the title of #${props.ticket.number} may have been changed. The title shown is what omo has.`,
      refused: 'omo refused the title.',
    })
  } finally {
    saving.value = false
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.isComposing) {
    e.preventDefault()
    void save()
  } else if (e.key === 'Escape') {
    // Escape leaves the title, not the panel.
    e.preventDefault()
    e.stopPropagation()
    endEdit()
  }
}

defineExpose({ discard: () => (editing.value = false) })
</script>

<template>
  <div class="head" data-ticket-header>
    <div class="head__meta">
      <TypeIcon :type="ticket.type" />
      <span class="head__number">#{{ ticket.number }}</span>
      <span class="head__sep" aria-hidden="true">·</span>
      <span class="head__column" data-ticket-column>{{ COLUMN_TITLES[ticket.column] }}</span>
      <span v-if="resolution" class="head__muted" data-ticket-resolution>{{ resolution }}</span>
      <span v-if="stateText" class="head__muted" data-ticket-caption>{{ stateText }}</span>
      <StatusBadge v-if="badge" :tone="badge.tone" :text="badge.text" :title="badge.title" data-ticket-badge />
    </div>
    <div v-if="!editing" class="head__title-row">
      <component :is="`h${level}`" :id="titleId" class="head__title" tabindex="-1" data-ticket-title>{{ shownTitle }}</component>
      <IconButton v-if="!readonly" ref="editButton" label="Edit title" size="sm" data-edit-title @click="startEdit">
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
          <path d="M1.5 10.5l.6-2.6 6.3-6.3 2 2-6.3 6.3zM7.4 2.6l2 2" fill="none" stroke="currentColor" stroke-width="1.2" />
        </svg>
      </IconButton>
    </div>
    <form v-else class="head__edit" novalidate @submit.prevent="save">
      <component :is="`h${level}`" :id="titleId" class="sr-only">{{ shownTitle }}</component>
      <label class="sr-only" :for="`${titleId}-input`">Title</label>
      <!-- A field that wraps (Enter saves, so the title stays one line). -->
      <textarea
        :id="`${titleId}-input`"
        ref="input"
        v-model="draft"
        class="omo-input head__input"
        name="title"
        rows="1"
        autocomplete="off"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="`${titleId}-help`"
        data-title-input
        @keydown="onKeydown"
        @input="((error = ''), grow())"
      />
      <div :id="`${titleId}-help`" class="head__help">
        <p v-if="error" class="head__error" role="alert" data-title-error>{{ error }}</p>
        <p v-if="changedMeanwhile" class="head__note" data-title-changed>
          Meanwhile the title was changed to “{{ ticket.title }}”. Saving replaces it with yours.
        </p>
        <p class="head__note">Enter saves, Escape cancels. {{ length }} of {{ TITLE_MAX }} characters.</p>
      </div>
      <div class="head__edit-actions">
        <button type="submit" class="omo-btn omo-btn--primary omo-btn--sm" :disabled="saving">{{ saving ? 'Saving…' : 'Save' }}</button>
        <button type="button" class="omo-btn omo-btn--sm" @click="endEdit">Cancel</button>
      </div>
    </form>
    <ul v-if="pullRequests.length > 0" class="head__prs" aria-label="Pull requests" data-pull-requests>
      <li v-for="pr in pullRequests" :key="pr.url">
        <a :href="pr.url" target="_blank" rel="noreferrer noopener">{{ pr.name ? `${pr.name}: ` : '' }}{{ pr.url }}</a>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.head {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.head__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.head__number {
  color: var(--omo-accent);
  letter-spacing: 0.06em;
}
.head__sep,
.head__muted {
  color: var(--omo-muted);
}
.head__column {
  font-weight: 700;
}
.head__title-row {
  display: flex;
  align-items: flex-start;
  gap: calc(var(--omo-space) * 2);
}
.head__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  line-height: var(--omo-text-xl-lh);
  overflow-wrap: anywhere;
}
.head__title:focus {
  outline: none;
}
.head__title:focus-visible {
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: var(--omo-focus-offset);
}
.head__edit {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
}
.head__input {
  width: 100%;
  overflow: hidden;
  resize: none;
  overflow-wrap: anywhere;
  font-size: var(--omo-text-lg);
  line-height: var(--omo-text-lg-lh);
  font-weight: 700;
}
.head__help {
  display: grid;
  gap: var(--omo-space);
}
.head__note,
.head__error {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.head__error {
  color: var(--omo-danger);
}
.head__edit-actions {
  display: flex;
  gap: calc(var(--omo-space) * 2);
}
.head__prs {
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.head__prs a {
  color: var(--omo-accent);
}
</style>
