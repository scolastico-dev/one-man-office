<script setup lang="ts">
// The editor of one prompt: its text, a live preview, omo's default beside it
// and Reset. One resource per prompt; the page shows one editor at a time.
// The text is plain text everywhere (never Markdown or HTML). A broken
// template is refused by the server at save, under the text, in the server's
// words.
import { computed, inject, nextTick, ref, useId, watch } from 'vue'
import { getPrompt, putPrompt, resetPrompt } from '../../../api/endpoints'
import type { Prompt, PromptKind, SettingsChangedData, SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import TextArea from '../../../components/ui/TextArea.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleLines } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { SETTINGS_NARROW } from '../settingsSections'
import { askFirst, useLeaveGuard, type Question } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import PromptPreview from './PromptPreview.vue'
import TemplateFields from './TemplateFields.vue'

const props = withDefaults(defineProps<{ name: string; title: string; focus?: boolean }>(), { focus: false })
const emit = defineEmits<{ dirty: [dirty: boolean] }>()

const { client } = useSession()
const announce = useAnnouncer()
const narrow = inject(SETTINGS_NARROW, ref(false))
const root = ref<HTMLElement>()
const defaultId = useId()

/** What the last answer said about the prompt besides its text. */
const kind = ref<PromptKind>('message')
const defaultText = ref('')
const customized = ref(false)
function take(p: Prompt) {
  kind.value = p.kind
  defaultText.value = p.default
  customized.value = p.customized
}

interface Draft {
  text: string
}

const fileOf = new RegExp(`(^|[\\\\/])${props.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\.md$`)

const r = useSettingsResource<Draft>({
  load: async () => {
    const { data } = await getPrompt(client, props.name)
    take(data)
    return { value: { text: data.text }, revision: data.revision }
  },
  save: async (d, revision) => {
    const { data, eventId } = await putPrompt(client, props.name, d.text, revision)
    take(data)
    return { value: { text: data.text }, revision: data.revision, eventId }
  },
  watches: (ev) => {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'prompts' && d.name === props.name
    }
    if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      return typeof d?.file === 'string' && fileOf.test(d.file)
    }
    return false
  },
})

useLeaveGuard(r.dirty)
watch(r.dirty, (d) => emit('dirty', d), { immediate: true })

const isInstructions = computed(() => kind.value === 'instructions')
const text = computed({
  get: () => r.draft.value?.text ?? '',
  set: (v: string) => {
    if (r.draft.value) r.draft.value.text = v
  },
})
const loaded = computed(() => r.draft.value !== null)
const loadFailed = computed(() => !loaded.value && !r.loading.value && r.error.value !== '')
const textError = computed(() => r.fieldErrors.value.text)
const unplaced = computed(() => unplacedErrors(r.fieldErrors.value, ['text']))

const showPreview = ref(false)
const showDefault = ref(false)
const resetting = ref(false)
const resetError = ref('')

const FIELD_HINTS = {
  instructions: 'Plain text. It is inserted into the role\'s prompt as it is. Saved empty, it is removed.',
  template: 'A template: fields and conditions are written in double braces. The server checks it when you save.',
} as const

const RESET_QUESTIONS: Record<'instructions' | 'other', Question> = {
  instructions: { title: 'Remove your instructions?', text: 'Your text is deleted. The prompt has no such part again.', confirm: 'Remove', cancel: 'Keep' },
  other: { title: "Reset to omo's default?", text: "Your text of this prompt is deleted. Agents get omo's own text again.", confirm: 'Reset', cancel: 'Keep mine' },
}

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}

async function reset() {
  if (resetting.value || r.revision.value === null) return
  if (!(await askFirst(RESET_QUESTIONS[isInstructions.value ? 'instructions' : 'other']))) return
  resetting.value = true
  resetError.value = ''
  // The draft is about to be replaced; a conflict would only get in the way.
  r.discard()
  try {
    await resetPrompt(client, props.name, r.revision.value)
    announce.polite(isInstructions.value ? 'Your instructions were removed.' : "Reset to omo's default.")
  } catch (e) {
    resetError.value = e instanceof Error && e.message !== '' ? e.message : 'The prompt could not be reset.'
    announce.assertive(resetError.value)
  } finally {
    resetting.value = false
    await r.reload()
  }
}

let focused = false
watch(
  loaded,
  async (is) => {
    if (!is || focused || !props.focus) return
    focused = true
    await nextTick()
    root.value?.querySelector<HTMLElement>('textarea')?.focus()
  },
  { immediate: true },
)
</script>

<template>
  <div ref="root" class="prompt-editor" :data-prompt-editor="name">
    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <p v-if="r.loading.value && !loaded" class="prompt-editor__wait" data-prompt-loading>Reading the prompt…</p>
    <Callout v-else-if="loadFailed" tone="danger" live="assertive" data-prompt-load-error>
      <p>{{ r.error.value }}</p>
      <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
    </Callout>

    <template v-if="loaded">
      <UnplacedErrors :errors="unplaced" />
      <div class="prompt-editor__cols" :class="{ 'prompt-editor__cols--two': showDefault }">
        <TextArea
          v-model="text"
          :label="title"
          :rows="narrow ? 10 : 16"
          spellcheck="false"
          :hint="isInstructions ? FIELD_HINTS.instructions : FIELD_HINTS.template"
          :error="textError"
          class="prompt-editor__area"
          data-prompt-text
        />
        <div v-if="showDefault && !isInstructions" class="prompt-editor__default" data-prompt-default>
          <p :id="defaultId" class="prompt-editor__default-label">omo's default</p>
          <pre class="prompt-editor__default-text" role="region" tabindex="0" :aria-labelledby="defaultId">{{ visibleLines(defaultText) }}</pre>
        </div>
      </div>

      <TemplateFields v-if="!isInstructions" />

      <div class="prompt-editor__buttons">
        <UiButton size="sm" :aria-expanded="showPreview" data-prompt-preview-toggle @click="showPreview = !showPreview">Preview</UiButton>
        <UiButton v-if="!isInstructions" size="sm" variant="ghost" :aria-expanded="showDefault" data-prompt-default-toggle @click="showDefault = !showDefault">Show default</UiButton>
        <UiButton v-if="customized" size="sm" variant="ghost" :disabled="resetting" data-prompt-reset @click="reset">
          {{ isInstructions ? 'Remove your instructions…' : "Reset to omo's default…" }}
        </UiButton>
      </div>
      <Callout v-if="resetError" tone="danger" data-reset-error>
        <p>{{ resetError }}</p>
      </Callout>

      <PromptPreview v-if="showPreview" :name="name" :text="text" />

      <SaveBar :dirty="r.dirty.value" :busy="r.saving.value" :status="r.status.value" :error="r.error.value || undefined" @save="save" @discard="r.discard()">
        <template v-if="r.status.value === 'conflict'">
          <UiButton size="sm" data-use-theirs @click="r.useTheirs()">Use the new value</UiButton>
          <UiButton size="sm" data-keep-mine @click="r.keepMine()">Keep mine</UiButton>
        </template>
      </SaveBar>
    </template>
  </div>
</template>

<style scoped>
.prompt-editor {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.prompt-editor__wait {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.prompt-editor__cols {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.prompt-editor__cols--two {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, calc(var(--omo-space) * 70)), 1fr));
}
.prompt-editor__area {
  min-width: 0;
}
.prompt-editor__default {
  min-width: 0;
  align-self: start;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
}
.prompt-editor__default-label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.prompt-editor__default-text {
  box-sizing: border-box;
  max-height: calc(var(--omo-space) * 90);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  /* A composited layer: the compositor then finds this scroll box under a wheel itself, where asking the main thread can drop the wheel. */
  will-change: transform;
  font-family: inherit;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.prompt-editor__buttons {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
</style>
