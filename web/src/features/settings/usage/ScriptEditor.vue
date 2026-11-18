<script setup lang="ts">
// One usage script: its Lua text, omo's default beside it, Reset (or Delete
// for a script omo has no default of), a short reference of the helpers, and
// a test run. A script runs as the user with the user's credentials, so it
// is edited here and nowhere else in the browser. It has its own resource
// (read with its revision, saved with If-Match, "0" for a new one, kept
// current while open) and its own Save bar. A script that does not compile is
// refused by the server under the text, in the server's words.
import { computed, onMounted, ref, useId, watch } from 'vue'
import { ApiError } from '../../../api/client'
import { getUsageScript, putUsageScript, resetUsageScript } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import { NO_REVISION, type ApiEvent, type SettingsChangedData, type SettingsInvalidData, type UsageScript, type UsageSettings } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import TextArea from '../../../components/ui/TextArea.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleLines, visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { askFirst, useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import ScriptTest from './ScriptTest.vue'
import { CONTRACT, HELPERS, NEW_SCRIPT, fileOfScript } from './usageForms'

const props = withDefaults(
  defineProps<{
    name: string
    /** The script is not stored yet: it starts from a template and is created with If-Match "0". */
    isNew?: boolean
    /** The profiles that use the script. */
    usedBy?: readonly string[]
    /** The stored limits, for the test's meters. */
    usage?: UsageSettings | null
    /** The id of this editor, which the list's button names in aria-controls. */
    formId?: string
  }>(),
  { isNew: false, usedBy: () => [], usage: null, formId: undefined },
)
const emit = defineEmits<{
  /** Whether the editor has unsaved edits. */
  dirty: [boolean]
  /** The script was deleted (one without a default). */
  deleted: []
  /** The editor of a script not stored yet is closed. */
  close: []
  /** omo is working on the script (a test, a reset or delete): the editor stays open meanwhile. */
  busy: [boolean]
}>()

const { client } = useSession()
const announce = useAnnouncer()
const ids = useId()
const titleId = `${ids}-title`
const defaultId = `${ids}-default`

const shownName = computed(() => visibleText(props.name))
const info = ref<Pick<UsageScript, 'default' | 'has_default' | 'customized'>>({ default: '', has_default: false, customized: false })
const stored = ref(false)
/** The script was stored at some point while the editor is open; it is gone now. */
let wasStored = false
const gone = ref(false)
const goneText = computed(() => `The usage script ${shownName.value} no longer exists.`)

function take(s: UsageScript) {
  info.value = { default: s.default, has_default: s.has_default, customized: s.customized }
  wasStored = true
  stored.value = true
  gone.value = false
}

interface Draft {
  text: string
}

const r = useSettingsResource<Draft>({
  async load() {
    try {
      const { data } = await getUsageScript(client, props.name)
      take(data)
      return { value: { text: data.text }, revision: data.revision }
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 404) throw e
      stored.value = false
      // A new script starts from the template, unsaved until it is saved; one
      // that was stored and is gone now (deleted by hand) says so instead.
      if (props.isNew && !wasStored) return { value: { text: NEW_SCRIPT }, revision: NO_REVISION, unsaved: true }
      gone.value = true
      throw new Error(goneText.value)
    }
  },
  async save(d, revision) {
    const { data, eventId } = await putUsageScript(client, props.name, d.text, revision)
    take(data)
    return { value: { text: data.text }, revision: data.revision, eventId }
  },
  watches(ev: ApiEvent) {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'usage' && d.name === props.name
    }
    if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      return typeof d?.file === 'string' && fileOfScript(d.file) === props.name
    }
    return false
  },
})

useLeaveGuard(r.dirty)
watch(r.dirty, (d) => emit('dirty', d), { immediate: true })

const root = ref<HTMLElement>()
const heading = ref<HTMLElement>()
onMounted(() => heading.value?.focus())

const text = computed({
  get: () => r.draft.value?.text ?? '',
  set: (v: string) => {
    if (r.draft.value) r.draft.value.text = v
  },
})
const loaded = computed(() => r.draft.value !== null)
const unplaced = computed(() => unplacedErrors(r.fieldErrors.value, ['text']))
const label = computed(() => `Save changes to the script ${shownName.value}`)

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}

const showDefault = ref(false)

// Reset to omo's default, or Delete for a script without one.

const resetting = ref(false)
const resetError = ref('')
const testing = ref(false)
watch(
  () => resetting.value || testing.value,
  (b) => emit('busy', b),
)
const canReset = computed(() => stored.value && (!info.value.has_default || info.value.customized))

async function reset() {
  if (resetting.value || r.revision.value === null) return
  const own = !info.value.has_default
  const ok = await askFirst(
    own
      ? { title: `Delete the script ${shownName.value}?`, text: 'omo removes its file. Profiles cannot use it any more.', confirm: 'Delete', cancel: 'Keep it' }
      : { title: "Reset to omo's default?", text: "Your text of this script is deleted. omo runs its own text again.", confirm: 'Reset', cancel: 'Keep mine' },
  )
  if (!ok) return
  resetting.value = true
  resetError.value = ''
  try {
    await resetUsageScript(client, props.name, r.revision.value)
    if (own) {
      announce.polite(`Script ${shownName.value} deleted.`)
      emit('deleted')
      return
    }
    // The draft is replaced by the default; a conflict would only get in the way.
    r.discard()
    announce.polite("Reset to omo's default.")
  } catch (e) {
    resetError.value = failureText(e, {
      lost: own ? 'No answer from omo, so the script may have been deleted. Look in the list.' : 'No answer from omo; the page shows what omo stored.',
      refused: own ? 'The script could not be deleted.' : 'The script could not be reset.',
    })
    announce.assertive(visibleText(resetError.value))
  } finally {
    resetting.value = false
    if (!own || resetError.value !== '') await r.reload()
  }
}
</script>

<template>
  <section :id="formId ?? `${ids}-form`" ref="root" class="script-editor" :aria-labelledby="titleId" :data-script-editor="name">
    <div class="script-editor__head">
      <h3 :id="titleId" ref="heading" class="script-editor__title" tabindex="-1">
        Script {{ shownName }}<span v-if="!stored && !r.loading.value && !gone" class="script-editor__new"> (not saved yet)</span>
      </h3>
      <UiButton v-if="!stored" size="sm" data-script-close @click="emit('close')">Close</UiButton>
    </div>

    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <Callout v-if="gone" tone="warn" live="polite" data-script-gone>
      <p>{{ goneText }}</p>
    </Callout>
    <p v-if="r.loading.value && !loaded" class="script-editor__note">Reading the script…</p>
    <Callout v-else-if="!loaded && r.error.value && !gone" tone="danger" live="assertive" data-settings-load-error>
      <p>{{ r.error.value }}</p>
      <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
    </Callout>

    <template v-if="loaded && !(gone && !r.dirty.value)">
      <UnplacedErrors :errors="unplaced" />
      <p v-if="stored && info.has_default && info.customized" class="script-editor__note" data-script-customized>
        Customized: omo runs your text instead of its own. A newer omo may ship a changed default, and omo does not tell you: compare with Show
        default, or take omo's current one with Reset.
      </p>
      <div class="script-editor__cols" :class="{ 'script-editor__cols--two': showDefault }">
        <div class="script-editor__code">
          <TextArea
            v-model="text"
            label="Lua"
            :rows="16"
            spellcheck="false"
            autocomplete="off"
            autocapitalize="off"
            wrap="off"
            hint="Runs as you, with the credentials of the profiles that use it. The server checks it when you save."
            :error="r.fieldErrors.value.text"
            data-script-text
          />
        </div>
        <div v-if="showDefault && info.has_default" class="script-editor__default" data-script-default>
          <p :id="defaultId" class="script-editor__default-label">omo's default</p>
          <pre class="script-editor__default-text" role="region" tabindex="0" :aria-labelledby="defaultId">{{ visibleLines(info.default) }}</pre>
        </div>
      </div>

      <div class="script-editor__buttons">
        <UiButton v-if="info.has_default" size="sm" :aria-expanded="showDefault" data-script-default-toggle @click="showDefault = !showDefault">Show default</UiButton>
        <UiButton v-if="canReset" size="sm" :variant="info.has_default ? 'ghost' : 'danger'" :disabled="resetting" data-script-reset @click="reset">
          {{ info.has_default ? "Reset to omo's default…" : `Delete ${shownName}…` }}
        </UiButton>
      </div>
      <Callout v-if="resetError" tone="danger" data-reset-error>
        <p>{{ visibleText(resetError) }}</p>
      </Callout>

      <details class="script-editor__ref">
        <summary>What a script returns, and the helpers it can call</summary>
        <p class="script-editor__ref-contract" data-script-contract>{{ CONTRACT }}</p>
        <dl class="script-editor__ref-list">
          <template v-for="[call, what] in HELPERS" :key="call">
            <dt><code>{{ call }}</code></dt>
            <dd>{{ what }}</dd>
          </template>
        </dl>
      </details>

      <ScriptTest :name="name" :draft="text" :used-by="usedBy" :usage="usage" @busy="(b) => (testing = b)" />

      <SaveBar
        :label="label"
        :dirty="r.dirty.value"
        :busy="r.saving.value"
        :status="r.status.value"
        :error="r.error.value ? visibleText(r.error.value) : undefined"
        @save="save"
        @discard="r.discard()"
      >
        <template v-if="r.status.value === 'conflict'">
          <UiButton size="sm" data-use-theirs @click="r.useTheirs()">Use the new value</UiButton>
          <UiButton size="sm" data-keep-mine @click="r.keepMine()">Keep mine</UiButton>
        </template>
      </SaveBar>
    </template>
  </section>
</template>

<style scoped>
/* The open script: a framed box under the list, like a workspace folder. */
.script-editor {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-bg);
}
.script-editor__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.script-editor__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
  overflow-wrap: anywhere;
}
.script-editor__title:focus-visible {
  outline-offset: var(--omo-space);
}
.script-editor__new,
.script-editor__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.script-editor__note {
  margin: 0;
}
.script-editor__cols {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.script-editor__cols--two {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, calc(var(--omo-space) * 70)), 1fr));
}
.script-editor__code {
  min-width: 0;
}
/* Scripts keep their lines; long ones scroll inside the box. */
.script-editor__code :deep(textarea) {
  white-space: pre;
  overflow-x: auto;
  overflow-wrap: normal;
}
.script-editor__default {
  min-width: 0;
  align-self: start;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.script-editor__default-label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.script-editor__default-text {
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
  white-space: pre;
}
.script-editor__default-text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.script-editor__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.script-editor__ref {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.script-editor__ref summary {
  justify-self: start;
  color: var(--omo-muted);
  cursor: pointer;
}
.script-editor__ref-contract {
  margin: calc(var(--omo-space) * 2) 0 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.script-editor__ref-list {
  display: grid;
  grid-template-columns: minmax(0, max-content) minmax(0, 1fr);
  gap: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 4);
  margin: calc(var(--omo-space) * 2) 0 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.script-editor__ref-list dt,
.script-editor__ref-list dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.script-editor__ref-list dd {
  color: var(--omo-muted);
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
