<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import { vDisabled } from '../../components/ui/vDisabled'
import { visibleText } from '../../components/ui/visibleText'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { guardPaste } from './terminalPaste'
import { editDraft, insertImage, markerRange, nextImageNumber, loadDraft, MAX_MESSAGE_CHARS, messageOf, saveDraft, type Attachment, type Draft } from './draftStore'
import { takeableImages, uploadImages } from './terminalUploads'

const props = defineProps<{
  /** The terminal's id: its draft is kept under it. */
  draftId: string
  /** Accessible name of the box. */
  label: string
  /** Puts one message into the terminal as a paste and submits it; rejects with the reason. */
  send: (message: string[]) => Promise<void>
}>()

const session = useSession()
const announcer = useAnnouncer()

const box = ref<HTMLElement | null>(null)
const field = ref<HTMLTextAreaElement | null>(null)
const initial = loadDraft(props.draftId)
const text = ref(initial.text)
const attachments = ref<Attachment[]>(initial.attachments)
const sending = ref(false)
const uploads = ref(0)
const uploading = computed(() => uploads.value > 0)
let imageNumber = nextImageNumber(initial)
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

function draft(): Draft { return { text: text.value, attachments: attachments.value } }
function apply(value: Draft): void {
  text.value = value.text
  attachments.value = value.attachments
}
function caret(at: number): void {
  void nextTick(() => field.value?.setSelectionRange(at, at))
}

/** All replacements treat a marker as one character, including cut and IME edits. */
function replace(start: number, end: number, value: string): void {
  const [from] = markerRange(draft(), start, end)
  apply(editDraft(draft(), start, end, value))
  caret(from + value.length)
}

/** Text pasted or dropped in goes where the cursor is. */
function insert(clean: string): void {
  if (sending.value) return
  const start = field.value?.selectionStart ?? text.value.length
  replace(start, field.value?.selectionEnd ?? start, clean)
}

let nativeEdit: { before: string; start: number; end: number; type: string } | null = null
function beforeInput(ev: InputEvent): void {
  const el = ev.target as HTMLTextAreaElement
  nativeEdit = { before: text.value, start: el.selectionStart, end: el.selectionEnd, type: ev.inputType }
}

/** Native textarea edits are reconciled against the previous marker positions. */
function onInput(ev: Event): void {
  const el = ev.target as HTMLTextAreaElement
  const value = el.value
  const before = text.value
  const edit = nativeEdit
  nativeEdit = null
  if (edit?.before === before && /^(insert|delete)/.test(edit.type)) {
    let { start, end } = edit
    if (start === end && edit.type.startsWith('delete')) {
      const removed = Math.max(0, before.length - value.length)
      if (edit.type.endsWith('Backward')) start -= removed
      else end += removed
    }
    const added = value.length - (before.length - (end - start))
    const replacement = value.slice(start, start + added)
    if (added >= 0 && value === before.slice(0, start) + replacement + before.slice(end)) {
      replace(start, end, replacement)
      el.value = text.value
      return
    }
  }
  let start = 0
  while (start < before.length && start < value.length && before[start] === value[start]) start++
  let end = before.length
  let tail = value.length
  while (end > start && tail > start && before[end - 1] === value[tail - 1]) { end--; tail-- }
  replace(start, end, value.slice(start, tail))
  // Vue may see an unchanged value after expanding a native edit to a whole marker.
  el.value = text.value
}

async function takeFiles(list: File[]): Promise<void> {
  if (sending.value) return
  const { images, problems } = takeableImages(list)
  error.value = problems.join(' ')
  const pending: Array<{ file: File; marker: string }> = []
  let start = field.value?.selectionStart ?? text.value.length
  let end = field.value?.selectionEnd ?? start
  for (const file of images) {
    const number = imageNumber++
    const [from] = markerRange(draft(), start, end)
    const marker = `[image ${number}]`
    apply(insertImage(draft(), start, end, { path: '', name: file.name || 'pasted image.png' }, number))
    pending.push({ file, marker })
    start = end = from + marker.length
  }
  caret(start)
  uploads.value += pending.length
  // Reserve every position before awaiting an upload, so later typing and other
  // pastes move the placeholders rather than choosing a new insertion point.
  for (const { file, marker } of pending) {
    try {
      const result = await uploadImages(session.client, [file])
      if (unmounted) continue
      const a = attachments.value.find((item) => item.marker === marker)
      if (a) {
        const stored = result.stored[0]
        if (stored) attachments.value = attachments.value.map((item) => item.marker === marker ? { ...item, ...stored } : item)
        else apply(editDraft(draft(), a.at, a.at + a.marker.length, ''))
      }
      problems.push(...result.problems)
      error.value = problems.join(' ')
    } finally {
      uploads.value--
    }
  }
  if (error.value && !unmounted) announcer.assertive(error.value)
}

function remove(i: number): void {
  if (sending.value) return
  const a = attachments.value[i]
  if (!a) return
  replace(a.at, a.at + a.marker.length, '')
  field.value?.focus()
}

async function submit(): Promise<void> {
  if (sending.value || uploading.value) return
  const sentText = text.value
  const sentFiles = attachments.value
  const message = messageOf(sentText, sentFiles)
  if (message.length === 0) return
  const size = message.reduce((sum, part) => sum + part.length, 0)
  if (size > MAX_MESSAGE_CHARS) {
    error.value = `A message has at most ${MAX_MESSAGE_CHARS} characters; this one has ${size}.`
    return
  }
  sending.value = true
  error.value = ''
  try {
    await props.send(message)
    // Only what was sent goes: anything added meanwhile stays.
    if (text.value === sentText) text.value = ''
    attachments.value = attachments.value.filter((a) => !sentFiles.some((f) => f.marker === a.marker))
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
  if (!sending.value && !ev.isComposing && (ev.key === 'Backspace' || ev.key === 'Delete')) {
    let start = field.value?.selectionStart ?? 0
    let end = field.value?.selectionEnd ?? start
    if (start === end) {
      if (ev.key === 'Backspace') start = Math.max(0, start - 1)
      else end = Math.min(text.value.length, end + 1)
    }
    if (attachments.value.some((a) => start < a.at + a.marker.length && end > a.at)) {
      ev.preventDefault()
      replace(start, end, '')
      return
    }
  }
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
      <li v-for="(a, i) in attachments" :key="a.marker" class="td__file" data-draft-attachment>
        <span class="td__name">{{ visibleText(a.name) }}</span>
        <button type="button" class="td__remove" :aria-label="`Remove ${visibleText(a.name)}`" v-disabled="sending" data-draft-remove @click="remove(i)">×</button>
      </li>
    </ul>
    <div class="td__row" data-height-free="the box grows with its text">
      <textarea
        ref="field"
        :value="text"
        @beforeinput="beforeInput"
        @input="onInput"
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
  align-items: stretch;
  gap: calc(var(--omo-space) * 2);
}
.td__field {
  flex: 1 1 auto;
  min-height: var(--omo-control-h);
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
