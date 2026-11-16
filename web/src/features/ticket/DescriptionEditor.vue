<script setup lang="ts">
// Edits a description as Markdown text, with a preview on demand. An edit is
// based on the version that was current when editing began: when someone
// else changed the description meanwhile, nothing is overwritten; the text
// typed here stays, and the user compares and decides. Images are added with
// "Add image…", a paste or a drop (imageUploads.ts).
import { computed, onMounted, ref, useId, watch } from 'vue'
import { ApiError } from '../../api/client'
import { setDescription, uploadImage } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { TicketSummary } from '../../api/types'
import TextArea from '../../components/ui/TextArea.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { ROLE_LABELS } from '../board/model'
import { STORY_TEMPLATE } from '../create/createRules'
import DescriptionView from './DescriptionView.vue'
import ImageAttach from '../images/ImageAttach.vue'
import { createImageUploads } from '../images/imageUploads'
import { ticketDrafts } from './drafts'

const props = defineProps<{
  ticket: TicketSummary
  /** The newest description the panel knows. */
  current: { text: string; version: number }
}>()
const emit = defineEmits<{
  close: []
  /** Saved as `version`; the text shows until the stream delivered event `eventId`. */
  saved: [text: string, eventId: number | null, version: number]
  dirty: [dirty: boolean]
  /** The server knows a newer version than `current`: load it. */
  refresh: []
}>()

const session = useSession()
const say = useAnnouncer()
const id = useId()

// An edit of this ticket begun in its other view (panel or page) goes on here.
const kept = ticketDrafts.description(props.ticket.number)
const initial = kept?.initial ?? (props.current.text === '' && props.ticket.type === 'story' ? STORY_TEMPLATE : props.current.text)
const draft = ref(kept?.text ?? initial)
/** The version this edit is based on. */
const base = ref(kept?.base ?? props.current.version)
const preview = ref(false)
const showTheirs = ref(false)
const saving = ref(false)
const error = ref('')
/** omo refused the edit because the description changed meanwhile. */
const refused = ref(false)
const area = ref<HTMLElement>()
const uploads = createImageUploads({
  upload: async (file, name) => (await uploadImage(session.client, props.ticket.number, file, name)).data,
  text: draft,
  area: () => area.value?.querySelector('textarea') ?? null,
  announce: (m, urgent) => (urgent ? say.assertive(m) : say.polite(m)),
})

const dirty = computed(() => draft.value !== initial)
watch(dirty, (d) => emit('dirty', d), { immediate: true })
// The text typed is kept for the ticket until it is saved or dropped (the owner of this editor forgets it then).
watch(draft, (text) => ticketDrafts.setDescription(props.ticket.number, text === initial ? null : { text, base: base.value, initial }))

/** Someone else saved a version since this edit began. */
const newer = computed(() => props.current.version !== base.value)
const conflict = computed(() => newer.value || refused.value)
/** The agent that is told about a new description (it may be parked: it reads it when it goes on); null without one. */
const agentTold = computed(() => {
  const role = props.ticket.type === 'story' ? 'pm' : 'developer'
  return props.ticket.agents.some((a) => a.role === role && a.state !== 'ended' && a.purpose !== 'export') ? ROLE_LABELS[role] : null
})

onMounted(() => {
  area.value?.querySelector('textarea')?.focus()
})

async function save(anyway = false) {
  if (saving.value || uploads.busy.value > 0) return
  error.value = ''
  // Never replace a version this edit has not seen without being asked to.
  if (newer.value && !anyway) {
    refused.value = true
    return
  }
  saving.value = true
  const text = draft.value
  try {
    const res = await setDescription(session.client, props.ticket.number, text, anyway ? props.current.version : base.value)
    say.polite(`Description of #${props.ticket.number} saved.`)
    emit('saved', text, res.eventId, res.data.number)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'stale_description') {
      refused.value = true
      const known = (e.details as { current_version?: unknown } | undefined)?.current_version
      if (typeof known !== 'number' || known > props.current.version) emit('refresh')
      say.assertive('Someone changed the description meanwhile. Your text is kept.')
    } else {
      error.value = failureText(e, {
        lost: 'No answer from omo, so the description may have been saved. Your text is kept; check the description before you save again.',
        refused: 'omo refused the description.',
      })
    }
  } finally {
    saving.value = false
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    void save()
  }
}

defineExpose({ save })
</script>

<template>
  <form class="editor" novalidate data-description-editor @submit.prevent="save()">
    <div class="editor__modes" role="group" aria-label="View">
      <button type="button" class="omo-btn omo-btn--sm" :aria-pressed="!preview" @click="preview = false">Write</button>
      <button type="button" class="omo-btn omo-btn--sm" :aria-pressed="preview" data-preview @click="preview = true">Preview</button>
    </div>
    <div v-if="conflict" class="editor__conflict" role="status" data-stale>
      <p class="editor__conflict-head">Someone changed the description meanwhile.</p>
      <p class="editor__text">Your text is kept here. Compare it with theirs, then save yours anyway or take theirs.</p>
      <div class="editor__row">
        <UiButton size="sm" :aria-expanded="showTheirs" :aria-controls="`${id}-theirs`" data-show-theirs @click="showTheirs = !showTheirs">
          {{ showTheirs ? 'Hide theirs' : 'Show theirs' }}
        </UiButton>
        <UiButton size="sm" variant="danger" :disabled="saving" data-save-anyway @click="save(true)">Save mine anyway</UiButton>
        <UiButton size="sm" data-take-theirs @click="emit('close')">Discard mine</UiButton>
      </div>
    </div>
    <div class="editor__panes" :class="{ 'editor__panes--two': conflict && showTheirs }">
      <div ref="area" class="editor__pane">
        <TextArea
          v-show="!preview"
          v-model="draft"
          :label="conflict && showTheirs ? 'Your description (Markdown)' : 'Description (Markdown)'"
          :rows="14"
          spellcheck="true"
          data-description-input
          @keydown="onKeydown"
          @paste="uploads.onPaste"
          @dragover="uploads.onDragOver"
          @drop="uploads.onDrop"
        />
        <ImageAttach v-show="!preview" class="editor__attach" :uploads="uploads" />
        <div v-if="preview" class="editor__preview" data-description-preview>
          <p class="editor__label">Preview</p>
          <DescriptionView v-if="draft.trim() !== ''" :text="draft" />
          <p v-else class="editor__muted">Nothing to preview.</p>
        </div>
      </div>
      <section v-if="conflict && showTheirs" :id="`${id}-theirs`" class="editor__pane editor__theirs" aria-label="Their description" data-theirs>
        <p class="editor__label">Theirs (version {{ current.version }})</p>
        <pre class="editor__source">{{ current.text }}</pre>
      </section>
    </div>
    <p v-if="agentTold" class="editor__muted" data-agent-told>The {{ agentTold }} is told about the change.</p>
    <p v-if="error" class="editor__error" role="alert" data-description-error>{{ error }}</p>
    <div class="editor__row">
      <UiButton type="submit" variant="primary" :disabled="saving || uploads.busy.value > 0" data-save-description>{{ saving ? 'Saving…' : 'Save' }}</UiButton>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <span class="editor__muted editor__keys">Ctrl+Enter saves.</span>
    </div>
  </form>
</template>

<style scoped>
.editor {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.editor__modes,
.editor__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.editor__conflict {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.editor__conflict-head {
  margin: 0;
  color: var(--omo-warn);
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.editor__text,
.editor__muted,
.editor__error,
.editor__label {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.editor__muted,
.editor__label {
  color: var(--omo-muted);
}
.editor__label {
  margin-bottom: calc(var(--omo-space) * 1.5);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.editor__error {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
}
.editor__panes {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.editor__panes--two {
  grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
}
.editor__pane {
  min-width: 0;
}
.editor__pane :deep(textarea) {
  width: 100%;
  min-height: 14rem;
}
.editor__attach {
  margin-top: calc(var(--omo-space) * 2);
}
.editor__preview {
  min-height: 14rem;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) dashed var(--omo-line-strong);
}
.editor__source {
  max-height: 24rem;
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.editor__keys {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
