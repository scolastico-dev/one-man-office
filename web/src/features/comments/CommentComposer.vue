<script setup lang="ts">
// Writes a comment on the ticket. It sends `body`, `question`, `to` and
// `reply_to` and nothing else; whether a mention or an addressee is allowed
// is the server's to say, and its message shows under the box with the text
// kept. Typing `@` offers the roles the board accepts on this ticket. A text
// above the server's size limit is refused here, before it is sent. Images
// are added with "Add image…", a paste or a drop (imageUploads.ts); a comment
// is not sent while one is still uploading.
import { computed, nextTick, ref, useId, watch } from 'vue'
import { failureText } from '../../api/lostAnswer'
import type { Comment, CommentRequest, Role } from '../../api/types'
import SelectField from '../../components/ui/SelectField.vue'
import TextArea from '../../components/ui/TextArea.vue'
import Toggle from '../../components/ui/Toggle.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { visibleText } from '../../components/ui/visibleText'
import type { TicketSummary } from '../../api/types'
import { ticketDrafts } from '../ticket/drafts'
import { submitKeyName } from '../../components/ui/submitKey'
import ImageAttach from '../images/ImageAttach.vue'
import { createImageUploads, type UploadImage } from '../images/imageUploads'
import { askTargets, mentionOptions, type MentionContext, type MentionOption } from './mentions'

/** The server's limit for a comment body, in UTF-8 bytes. */
const MAX_BYTES = 64 * 1024

const props = defineProps<{
  ticket: TicketSummary
  context: MentionContext
  /** Sends the comment; resolves with the roles nobody works as. */
  send(req: CommentRequest): Promise<Role[]>
  /** The open question the comment answers. */
  answering: Comment | null
  /** Uploads an image to the ticket; without it no image can be added. */
  upload?: UploadImage
}>()
const emit = defineEmits<{ 'stop-answering': []; sent: [] }>()

const announcer = useAnnouncer()
const uid = useId()

// The comment being written is kept per ticket, so it survives switching tabs and between the ticket's panel and page.
const text = ref(ticketDrafts.comment(props.ticket.number))
watch(text, (t) => ticketDrafts.setComment(props.ticket.number, t))
const asking = ref(false)
const to = ref('')
const busy = ref(false)
const error = ref('')
const note = ref('')
const area = ref<{ $el: HTMLElement } | null>(null)

const targets = computed(() => askTargets(props.ticket, props.context))
const targetOptions = computed(() => targets.value.map((t) => ({ value: t.to, label: t.label })))
watch(
  targets,
  (list) => {
    if (!list.some((t) => t.to === to.value)) to.value = list[0]?.to ?? ''
    if (list.length === 0) asking.value = false
  },
  { immediate: true },
)
watch(
  () => props.answering,
  (q) => {
    if (q) asking.value = false
  },
)

const options = computed(() => mentionOptions(props.ticket, props.context))
const hint = computed(() => {
  const named = options.value.filter((o) => o.name !== undefined).map((o) => `@${o.role} is ${visibleText(o.name!)}`)
  return named.length > 0 ? `Mention a role to reach its agent: ${named.join(', ')}.` : 'Mention a role with @ to reach its agent.'
})

const label = computed(() => (props.answering ? 'Answer' : asking.value ? 'Ask' : 'Comment'))
const uploads = createImageUploads({
  upload: (file, name) => props.upload!(file, name),
  text,
  area: () => textarea(),
  announce: (m, urgent) => (urgent ? announcer.assertive(m) : announcer.polite(m)),
})
const canSend = computed(
  () => text.value.trim() !== '' && !busy.value && uploads.busy.value === 0 && (!asking.value || to.value !== ''),
)

// @ completion

const caret = ref(0)
const listId = `${uid}-mentions`
const dismissed = ref(false)
const active = ref(0)

/** `@` and the letters typed after it, when the caret stands right behind them. */
const token = computed(() => {
  const m = /(^|[^\p{L}\p{N}_.@/`-])@([A-Za-z]*(?::[a-z0-9._-]*)?)$/u.exec(text.value.slice(0, caret.value))
  return m ? { prefix: (m[2] ?? '').toLowerCase(), start: caret.value - (m[2] ?? '').length - 1 } : null
})
const choices = computed<MentionOption[]>(() => {
  const t = token.value
  if (!t || dismissed.value) return []
  return options.value.filter((o) => o.role.startsWith(t.prefix))
})
const open = computed(() => choices.value.length > 0)
watch([choices, () => token.value?.prefix], () => {
  active.value = 0
})
const optionId = (i: number) => `${listId}-${i}`
// A text box cannot say that a list is open; the list is announced instead.
watch(open, (isOpen) => {
  if (isOpen) announcer.polite(`${choices.value.length} roles to mention. Use the arrow keys and Enter.`)
})

function textarea(): HTMLTextAreaElement | null {
  return (area.value?.$el ?? null)?.querySelector('textarea') ?? null
}

function syncCaret() {
  const el = textarea()
  if (el) caret.value = el.selectionStart
}

function onInput() {
  dismissed.value = false
  error.value = ''
  syncCaret()
}

function choose(o: MentionOption) {
  const t = token.value
  const el = textarea()
  if (!t || !el) return
  const before = text.value.slice(0, t.start)
  const after = text.value.slice(caret.value)
  text.value = `${before}@${o.role} ${after}`
  const at = before.length + o.role.length + 2
  dismissed.value = false
  void nextTick(() => {
    el.focus()
    el.setSelectionRange(at, at)
    caret.value = at
  })
}

function onKeydown(e: KeyboardEvent) {
  if (open.value) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const n = choices.value.length
      active.value = (active.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
      return
    }
    if ((e.key === 'Enter' && !e.ctrlKey && !e.metaKey) || e.key === 'Tab') {
      if (e.shiftKey && e.key === 'Tab') return
      e.preventDefault()
      choose(choices.value[active.value]!)
      return
    }
    if (e.key === 'Escape') {
      // Closes the list, not the panel.
      e.preventDefault()
      e.stopPropagation()
      dismissed.value = true
      return
    }
  }
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    void submit()
  }
}

// Sending

async function submit() {
  if (!canSend.value) return
  const size = new TextEncoder().encode(text.value.trim()).length
  if (size > MAX_BYTES) {
    error.value = `The comment is ${Math.ceil(size / 1024)} KiB; omo takes at most 64 KiB. Shorten it or split it into several comments.`
    announcer.assertive(error.value)
    return
  }
  const req: CommentRequest = { body: text.value, question: asking.value }
  if (asking.value) req.to = to.value
  if (props.answering) req.reply_to = props.answering.id
  busy.value = true
  error.value = ''
  note.value = ''
  try {
    const unreachable = await props.send(req)
    text.value = ''
    caret.value = 0
    asking.value = false
    emit('sent')
    if (unreachable.length > 0) {
      const roles = unreachable.map((r) => `@${r}`).join(', ')
      note.value = `Nobody works as ${roles} on #${props.ticket.number} right now; the comment is stored.`
      announcer.polite(note.value)
    } else announcer.polite('Comment sent.')
  } catch (e) {
    error.value = failureText(e, {
      lost: 'The answer from omo was lost, so the comment may have been posted. Your text is kept; look at the thread before you send it again.',
      refused: 'The comment could not be sent.',
    })
    announcer.assertive(error.value)
  } finally {
    busy.value = false
  }
}

/** Puts the caret in the box (after "Answer"). */
function focus() {
  textarea()?.focus()
}
defineExpose({ focus })
</script>

<template>
  <form class="composer" data-composer @submit.prevent="submit">
    <div v-if="answering" class="composer__answering" data-answering>
      <span>Answering question #{{ answering.id }}</span>
      <UiButton size="sm" data-stop-answering @click="emit('stop-answering')">Stop answering</UiButton>
    </div>
    <div class="composer__box">
      <TextArea
        ref="area"
        v-model="text"
        label="Write a comment"
        :rows="3"
        :hint="hint"
        :error="error"
        aria-autocomplete="list"
        :aria-controls="open ? listId : undefined"
        :aria-activedescendant="open ? optionId(active) : undefined"
        data-comment-box
        @input="onInput"
        @keyup="syncCaret"
        @click="syncCaret"
        @keydown="onKeydown"
        @blur="dismissed = true"
        @focus="dismissed = false"
        @paste="upload && uploads.onPaste($event)"
        @dragover="upload && uploads.onDragOver($event)"
        @drop="upload && uploads.onDrop($event)"
      />
      <ul v-if="open" :id="listId" class="composer__list" role="listbox" aria-label="Roles to mention">
        <li
          v-for="(o, i) in choices"
          :id="optionId(i)"
          :key="o.role"
          role="option"
          class="composer__option"
          :aria-selected="i === active ? 'true' : 'false'"
          @mousedown.prevent="choose(o)"
        >
          <span>@{{ o.role }}</span>{{ ' ' }}<span v-if="o.project || o.name" class="composer__name"
            ><template v-if="o.project">{{ visibleText(o.project) }}</template
            ><template v-if="o.project && o.name">, </template
            ><template v-if="o.name">{{ visibleText(o.name) }}</template></span
          >
        </li>
      </ul>
    </div>
    <ImageAttach v-if="upload" :uploads="uploads" />
    <p v-if="note" class="composer__note" data-unreachable>{{ note }}</p>
    <div class="composer__ask">
      <Toggle
        v-model="asking"
        label="Ask a question"
        :disabled="targets.length === 0 || answering !== null"
      />
      <SelectField v-if="asking" v-model="to" label="Ask" :options="targetOptions" />
      <p v-if="targets.length === 0 && !answering" class="composer__hint">Nobody on this ticket can be asked: there is no product manager and the CEO is off.</p>
    </div>
    <div class="composer__actions">
      <UiButton variant="primary" type="submit" :disabled="!canSend" data-send>{{ busy ? 'Sending…' : label }}</UiButton>
    </div>
    <p class="composer__hint">{{ submitKeyName() }} sends.</p>
  </form>
</template>

<style scoped>
.composer {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin-top: calc(var(--omo-space) * 5);
  padding-top: calc(var(--omo-space) * 5);
  border-top: var(--omo-border) solid var(--omo-line);
}
.composer__box {
  position: relative;
}
.composer__list {
  position: absolute;
  z-index: 2;
  left: 0;
  right: 0;
  bottom: 100%;
  margin: 0 0 var(--omo-space);
  padding: 0;
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  list-style: none;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.composer__option {
  display: flex;
  gap: calc(var(--omo-space) * 3);
  justify-content: space-between;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  cursor: pointer;
}
.composer__option[aria-selected='true'] {
  background: var(--omo-select);
}
.composer__name {
  color: var(--omo-muted);
}
.composer__answering {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-warn);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.composer__ask {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: calc(var(--omo-space) * 3);
}
.composer__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2);
}
.composer__note,
.composer__hint {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
