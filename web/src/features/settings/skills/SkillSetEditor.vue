<script setup lang="ts">
// One skill set's form: its repository, branch or tag, how it updates, whether
// agents are told about it and what each role is told. It has its own
// resource (loaded with its revision, saved with If-Match, kept current while
// open) and its own Save bar. "Update now" fetches the stored set at once and
// shows the answer; the checkout's state comes from the page, which follows
// the server's events. Every text the server wrote is shown as visible text.
import { computed, onMounted, ref, useId, watch } from 'vue'
import { deleteSkillSet, getSkillSets, putSkillSet, updateSkillSet } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import { NO_REVISION, type ApiEvent, type SettingsChangedData, type SettingsInvalidData, type SkillSetState } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import RelativeTime from '../../../components/ui/RelativeTime.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import TextArea from '../../../components/ui/TextArea.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { askFirst, useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import {
  PLACED_FIELDS,
  SKILL_ROLES,
  UPDATE_OPTIONS,
  errorsOfSkillSet,
  fileOfSkillSet,
  missingState,
  shortCommit,
  toDraft,
  toSkillSet,
  updateResult,
  type SkillDraft,
} from './skillForms'

const props = withDefaults(
  defineProps<{
    name: string
    /** For a set not stored yet: where its form starts. */
    initial?: SkillDraft | null
    /** What omo knows of the checkout; absent until read. */
    state?: SkillSetState | null
    /** The id of this form, which the list's button names in aria-controls. */
    formId?: string
  }>(),
  { initial: null, state: null, formId: undefined },
)
const emit = defineEmits<{
  /** Whether the form has unsaved edits. */
  dirty: [boolean]
  /** The set was deleted. */
  deleted: []
  /** The form is closed. */
  close: []
  /** "Update now" answered with the set's state. */
  state: [SkillSetState]
  /** omo is working on the set for this form (Update now, Delete): the form stays open meanwhile. */
  busy: [boolean]
}>()

const session = useSession()
const announce = useAnnouncer()
const fallbackId = useId()

/** The set was stored at some point while the form is open; its file is gone now. */
let wasStored = false
const stored = ref(false)
const gone = ref(false)
const shownName = computed(() => visibleText(props.name))
const goneText = computed(() => `The skill set ${shownName.value} no longer exists.`)

const r = useSettingsResource<SkillDraft>({
  async load() {
    const { data } = await getSkillSets(session.client)
    const errors = errorsOfSkillSet(data.errors ?? [], props.name)
    const found = data.skill_sets.find((x) => x.name === props.name)
    if (found !== undefined) {
      wasStored = true
      stored.value = true
      gone.value = false
      return { value: toDraft(found), revision: found.revision ?? NO_REVISION, errors }
    }
    stored.value = false
    // A new set starts from its own form, unsaved until it is saved; one that
    // was stored and is gone now (deleted by hand) says so instead.
    if (props.initial !== null && !wasStored) {
      return { value: { ...props.initial, instructions: { ...props.initial.instructions } }, revision: NO_REVISION, errors, unsaved: true }
    }
    gone.value = true
    throw new Error(goneText.value)
  },
  async save(draft, revision) {
    const { data, eventId } = await putSkillSet(session.client, toSkillSet(draft), revision)
    wasStored = true
    stored.value = true
    gone.value = false
    return { value: toDraft(data), revision: data.revision ?? NO_REVISION, eventId }
  },
  watches(ev: ApiEvent) {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'skillsets' && d.name === props.name
    }
    if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      return typeof d?.file === 'string' && fileOfSkillSet(d.file) === props.name
    }
    return false
  },
})

useLeaveGuard(r.dirty)
watch(r.dirty, (d) => emit('dirty', d), { immediate: true })

const root = ref<HTMLElement>()
const heading = ref<HTMLElement>()
onMounted(() => heading.value?.focus())

const draft = computed(() => r.draft.value)
const fe = computed(() => r.fieldErrors.value)
const unplaced = computed(() => unplacedErrors(fe.value, PLACED_FIELDS))
const checkout = computed(() => props.state ?? missingState(props.name))
const label = computed(() => `Save changes to the skill set ${shownName.value}`)

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}

// Update now

const updating = ref(false)
const updated = ref<{ ok: boolean; text: string } | null>(null)
const running = computed(() => updating.value || checkout.value.state === 'updating')

async function updateNow() {
  if (updating.value) return
  updating.value = true
  updated.value = null
  try {
    const { data } = await updateSkillSet(session.client, props.name)
    emit('state', data)
    updated.value = updateResult(data)
  } catch (e) {
    updated.value = {
      ok: false,
      text: failureText(e, { lost: 'No answer from omo; the update may still be running. Look at the state above.', refused: 'The update was refused.' }),
    }
  } finally {
    updating.value = false
  }
  announce.polite(visibleText(updated.value?.text ?? ''))
}

// Delete

const deleteError = ref('')
const deleting = ref(false)

watch(
  () => updating.value || deleting.value,
  (b) => emit('busy', b),
)
async function remove() {
  const ok = await askFirst({
    title: `Delete the skill set ${shownName.value}?`,
    text: 'omo removes its settings and its checkout from this computer. Agents are no longer told about it.',
    confirm: 'Delete',
    cancel: 'Keep it',
  })
  if (!ok) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deleteSkillSet(session.client, props.name, r.revision.value ?? NO_REVISION)
    announce.polite(`Skill set ${shownName.value} deleted.`)
    emit('deleted')
  } catch (e) {
    deleteError.value = failureText(e, {
      lost: 'No answer from omo, so the skill set may have been deleted. Look in the list.',
      refused: 'The skill set could not be deleted.',
    })
    announce.assertive(visibleText(deleteError.value))
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section :id="formId ?? fallbackId" ref="root" class="skill-editor" :aria-labelledby="`${fallbackId}-title`" :data-skillset-editor="name">
    <div class="skill-editor__head">
      <h3 :id="`${fallbackId}-title`" ref="heading" class="skill-editor__title" tabindex="-1">
        Skill set {{ shownName }}<span v-if="!stored && !r.loading.value && !gone" class="skill-editor__new"> (not saved yet)</span>
      </h3>
      <UiButton v-if="!stored" size="sm" data-skillset-close @click="emit('close')">Close</UiButton>
    </div>

    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <Callout v-if="gone" tone="warn" live="polite" data-skillset-gone>
      <p>{{ goneText }}</p>
    </Callout>
    <p v-if="r.loading.value && !draft" class="skill-editor__note">Reading the skill set…</p>
    <Callout v-else-if="!draft && r.error.value && !gone" tone="danger" live="assertive" data-settings-load-error>
      <p>{{ r.error.value }}</p>
      <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
    </Callout>

    <template v-if="draft && !(gone && !r.dirty.value)">
      <UnplacedErrors :errors="unplaced" />
      <TextField
        v-model="draft.repository"
        label="Repository"
        hint="https, ssh or a local folder"
        :error="fe['repository']"
        spellcheck="false"
        autocomplete="off"
        autocapitalize="off"
        data-skillset-repository
      />
      <TextField
        v-model="draft.ref"
        label="Branch or tag"
        hint="Empty: the default branch."
        :error="fe['ref']"
        spellcheck="false"
        autocomplete="off"
        autocapitalize="off"
      />
      <SelectField v-model="draft.update" label="Updates" :options="UPDATE_OPTIONS" :error="fe['update']" />
      <Toggle v-model="draft.enabled" label="Agents are told about it" />

      <div v-if="stored" class="skill-editor__state" data-skillset-checkout>
        <p class="skill-editor__state-text">
          <template v-if="checkout.state === 'updating'">Updating…</template>
          <template v-else-if="checkout.state === 'ok'">
            Checked out at <code>{{ shortCommit(checkout.commit) }}</code
            ><template v-if="checkout.updated_at">, updated <RelativeTime :at="checkout.updated_at" /></template>, in
            <span class="skill-editor__path">{{ visibleText(checkout.path) }}</span>
          </template>
          <template v-else-if="checkout.state === 'failed'">
            The last update failed<template v-if="checkout.commit !== ''"> (still at <code>{{ shortCommit(checkout.commit) }}</code>)</template>:
            <span class="skill-editor__failure">{{ visibleText(checkout.message) }}</span>
          </template>
          <template v-else>Not checked out yet.</template>
        </p>
        <UiButton size="sm" :disabled="running" :aria-busy="running ? 'true' : undefined" data-skillset-update @click="updateNow">
          {{ running ? 'Updating…' : 'Update now' }}
        </UiButton>
      </div>
      <p v-if="updated" class="skill-editor__result" :class="{ 'skill-editor__result--bad': !updated.ok }" role="status" data-skillset-result>
        {{ visibleText(updated.text) }}
      </p>

      <FormSection
        legend="What agents are told"
        :level="4"
        description="Every agent of a role gets its text, with the folder's path above it. A role with no text is not told about this set."
      >
        <TextArea
          v-for="role in SKILL_ROLES"
          :key="role.id"
          v-model="draft.instructions[role.id]"
          :label="role.label"
          :rows="2"
          :error="fe[`instructions.${role.id}`]"
          :data-skillset-text="role.id"
        />
      </FormSection>

      <Callout v-if="deleteError" tone="danger" live="assertive" data-delete-error>
        <p>{{ visibleText(deleteError) }}</p>
      </Callout>
      <div v-if="stored">
        <UiButton size="sm" variant="danger" :disabled="deleting" data-skillset-delete @click="remove">Delete {{ shownName }}…</UiButton>
      </div>

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
/* The open set: a framed box under the list, like a workspace folder. */
.skill-editor {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-bg);
}
.skill-editor__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.skill-editor__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
  overflow-wrap: anywhere;
}
.skill-editor__title:focus-visible {
  outline-offset: var(--omo-space);
}
.skill-editor__new,
.skill-editor__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.skill-editor__note {
  margin: 0;
}
.skill-editor__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
}
.skill-editor__state-text {
  flex: 1 1 calc(var(--omo-space) * 60);
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.skill-editor__path {
  color: var(--omo-fg);
  overflow-wrap: anywhere;
}
.skill-editor__failure {
  color: var(--omo-danger);
}
.skill-editor__result {
  margin: 0;
  color: var(--omo-ok);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.skill-editor__result--bad {
  color: var(--omo-danger);
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
