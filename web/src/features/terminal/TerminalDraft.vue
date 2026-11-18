<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { guardPaste } from './terminalPaste'
import { loadDraft, MAX_MESSAGE_CHARS, messageOf, pasteOf, saveDraft, type Attachment } from './draftStore'
import { uploadImages } from './terminalUploads'

const props = defineProps<{
  /** The terminal's id: its draft is kept under it. */
  draftId: string
  /** Accessible name of the box. */
  label: string
  /** Whether the program in the terminal asked for bracketed paste. */
  bracketed: () => boolean
  /** Puts one message (already a paste) into the terminal and submits it; rejects with the reason. */
  send: (paste: string) => Promise<void>
}>()

const session = useSession()
const announcer = useAnnouncer()

const box = ref<HTMLElement | null>(null)
const field = ref<HTMLTextAreaElement | null>(null)
const initial = loadDraft(props.draftId)
const text = ref(initial.text)
const attachments = ref<Attachment[]>(initial.attachments)
const sending = ref(false)
const uploading = ref(false)
const error = ref('')
const dropping = ref(false)
let dragDepth = 0
let stopPaste: (() => void) | null = null
let unmounted = false

/** The box grows with its text up to a height of its own, then scrolls. */
function grow(): void {
  const el = field.value
  if (!el) return
  // The style's max-height caps it, and it then scrolls.
  el.style.height = 'auto'
  el.style.height = `${el.scrollHeight}px`
}

watch(text, () => void nextTick(grow))
watch([text, attachments], () => saveDraft(props.draftId, { text: text.value, attachments: attachments.value }), { deep: true })

/** Text pasted or dropped in goes where the cursor is. */
function insert(clean: string): void {
  const el = field.value
  if (!el) {
    text.value += clean
    return
  }
  const start = el.selectionStart ?? text.value.length
  const end = el.selectionEnd ?? start
  text.value = text.value.slice(0, start) + clean + text.value.slice(end)
  void nextTick(() => {
    el.selectionStart = el.selectionEnd = start + clean.length
  })
}

async function takeFiles(list: File[]): Promise<void> {
  uploading.value = true
  error.value = ''
  try {
    const { stored, problems } = await uploadImages(session.client, list)
    if (unmounted) return
    attachments.value = [...attachments.value, ...stored]
    if (problems.length > 0) {
      error.value = problems.join(' ')
      announcer.assertive(error.value)
    }
  } finally {
    uploading.value = false
  }
}

function remove(i: number): void {
  attachments.value = attachments.value.filter((_, at) => at !== i)
  field.value?.focus()
}

async function submit(): Promise<void> {
  if (sending.value || uploading.value) return
  const message = messageOf(text.value, attachments.value.map((a) => a.path))
  if (message === '') return
  if (message.length > MAX_MESSAGE_CHARS) {
    error.value = `A message has at most ${MAX_MESSAGE_CHARS} characters; this one has ${message.length}.`
    return
  }
  sending.value = true
  error.value = ''
  try {
    await props.send(pasteOf(message, props.bracketed()))
    text.value = ''
    attachments.value = []
    announcer.polite('Message sent.')
  } catch (e) {
    // The draft stays as it is.
    error.value = e instanceof Error ? e.message : 'The message was not sent.'
    announcer.assertive(error.value)
  } finally {
    sending.value = false
    if (!unmounted) void nextTick(() => field.value?.focus())
  }
}

function onKey(ev: KeyboardEvent): void {
  if (ev.key !== 'Enter' || ev.shiftKey || ev.isComposing) return
  ev.preventDefault()
  void submit()
}

function hasFiles(ev: DragEvent): boolean {
  return ev.dataTransfer?.types.includes('Files') === true
}
function onDragEnter(ev: DragEvent): void {
  if (!hasFiles(ev)) return
  dragDepth++
  dropping.value = true
}
function onDragLeave(ev: DragEvent): void {
  if (!hasFiles(ev) || dragDepth === 0) return
  if (--dragDepth === 0) dropping.value = false
}
function onDropEnd(): void {
  dragDepth = 0
  dropping.value = false
}

onMounted(() => {
  if (box.value) stopPaste = guardPaste(box.value, insert, (list) => void takeFiles(list))
  grow()
})
onBeforeUnmount(() => {
  unmounted = true
  stopPaste?.()
  saveDraft(props.draftId, { text: text.value, attachments: attachments.value })
})

defineExpose({ focus: () => field.value?.focus() })
</script>

<template>
  <div
    ref="box"
    class="td"
    :data-drop="dropping ? '' : undefined"
    data-terminal-draft
    @dragenter="onDragEnter"
    @dragleave="onDragLeave"
    @drop.capture="onDropEnd"
  >
    <ul v-if="attachments.length > 0" class="td__files" aria-label="Attached images">
      <li v-for="(a, i) in attachments" :key="a.path" class="td__file" data-draft-attachment>
        <span class="td__name">{{ visibleText(a.name) }}</span>
        <button type="button" class="td__remove" :aria-label="`Remove ${visibleText(a.name)}`" data-draft-remove @click="remove(i)">×</button>
      </li>
    </ul>
    <div class="td__row" data-height-free="the box grows with its text">
      <textarea
        ref="field"
        v-model="text"
        class="omo-input td__field"
        rows="1"
        :aria-label="label"
        :readonly="sending"
        :aria-busy="sending ? 'true' : undefined"
        placeholder="Write a message. Enter sends, Shift+Enter starts a new line."
        data-draft-field
        @keydown="onKey"
      />
      <UiButton variant="primary" :disabled="sending || uploading" data-draft-send @click="submit">{{ sending ? 'Sending…' : 'Send' }}</UiButton>
    </div>
    <p v-if="uploading" class="td__note" role="status">Uploading…</p>
    <p v-if="error" class="td__error" role="alert" data-draft-error>{{ error }}</p>
  </div>
</template>

<style scoped>
.td {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
.td[data-drop] {
  outline: var(--omo-border) dashed var(--omo-accent);
  outline-offset: calc(var(--omo-space) * 0.5);
}
.td__row {
  display: flex;
  align-items: flex-end;
  gap: calc(var(--omo-space) * 2);
}
.td__field {
  flex: 1 1 auto;
  resize: none;
  max-height: min(200px, 25dvh);
  overflow-y: auto;
}
.td__files {
  display: flex;
  flex-wrap: wrap;
  gap: var(--omo-space);
  margin: 0;
  padding: 0;
  list-style: none;
}
.td__file {
  display: inline-flex;
  align-items: center;
  gap: var(--omo-space);
  max-width: 100%;
  padding: calc(var(--omo-space) * 0.5) calc(var(--omo-space) * 1.5);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.td__name {
  min-width: 0;
  overflow-wrap: anywhere;
}
.td__remove {
  border: 0;
  background: none;
  color: var(--omo-muted);
  font: inherit;
  cursor: pointer;
}
.td__remove:hover,
.td__remove:focus-visible {
  color: var(--omo-fg);
}
.td__note,
.td__error {
  margin: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.td__note {
  color: var(--omo-muted);
}
.td__error {
  color: var(--omo-danger);
}
</style>
