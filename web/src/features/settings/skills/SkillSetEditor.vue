<script setup lang="ts">
// One skill set's form: its repository, branch or tag, how it updates, whether
// agents are told about it, which roles get it, which of its folders and
// files they get and what each of those roles is told. It has its own
// resource (loaded with its revision, saved with If-Match, kept current while
// open) and its own Save bar. "Update now" fetches the stored set at once and
// shows the answer; the checkout's state comes from the page, which follows
// the server's events. Every text the server wrote is shown as visible text.
import { computed, onMounted, ref, useId, watch } from 'vue'
import { deleteSkillSet, getSkillSets, putSkillSet, updateSkillSet } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import { NO_REVISION, type ApiEvent, type SettingsChangedData, type SettingsInvalidData, type SkillSetState } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import Chip from '../../../components/ui/Chip.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
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
import { rowErrors } from '../agents/profileForm'
import { shortCommit } from '../extensionWords'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { askFirst, useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import {
  SKILL_ROLES,
  UPDATE_OPTIONS,
  errorsOfSkillSet,
  fileOfSkillSet,
  isPlacedField,
  missingState,
  rolesError,
  rolesGetting,
  toDraft,
  toSkillSet,
  toggleRole,
  updateResult,
  type SkillDraft,
  type SkillRole,
} from './skillForms'
import { folderNote, SEARCH_TRUST_WARNING, type SkillOrigin } from './skillSearch'

const props = withDefaults(
  defineProps<{
    name: string
    /** For a set not stored yet: where its form starts. */
    initial?: SkillDraft | null
    /** For a set from a search on skills.sh that is not stored yet: where it came from (a warning shows, and saving asks first). */
    origin?: SkillOrigin | null
    /** What omo knows of the checkout; absent until read. */
    state?: SkillSetState | null
    /** The id of this form, which the list's button names in aria-controls. */
    formId?: string
  }>(),
  { initial: null, origin: null, state: null, formId: undefined },
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
      const start = props.initial
      return { value: { ...start, paths: [...start.paths], roles: [...start.roles], instructions: { ...start.instructions } }, revision: NO_REVISION, errors, unsaved: true }
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
const unplaced = computed(() => unplacedErrors(fe.value, isPlacedField))
const rolesId = `${fallbackId}-roles`
const rolesHintId = `${fallbackId}-roles-hint`
const rolesErrorId = `${fallbackId}-roles-error`
const rolesRefusal = computed(() => rolesError(fe.value))
const pathErrors = computed(() => rowErrors(fe.value, 'paths'))
const textRoles = computed(() => (draft.value ? rolesGetting(draft.value) : SKILL_ROLES))
/** The set's stored paths its checkout does not have; only while the form shows the stored paths. */
const missing = computed(() => (props.state?.missing_paths ?? []).filter((p) => draft.value?.paths.includes(p)))
function flip(role: SkillRole) {
  if (draft.value) draft.value.roles = toggleRole(draft.value, role)
}
const checkout = computed(() => props.state ?? missingState(props.name))
const label = computed(() => `Save changes to the skill set ${shownName.value}`)

/** The warning about a set from a search, while it is not stored. */
const found = computed(() => (stored.value ? null : props.origin))

async function save() {
  if (found.value !== null) {
    const ok = await askFirst({
      title: SEARCH_TRUST_WARNING.title,
      text: `${SEARCH_TRUST_WARNING.paragraphs.join(' ')} Save the skill set ${shownName.value} from ${visibleText(found.value.source)}?`,
      confirm: 'Save',
      cancel: 'Not yet',
    })
    if (!ok) return
  }
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}

// Update now

const updating = ref(false)
const updated = ref<{ ok: boolean; text: string } | null>(null)
const updateError = ref('')
const running = computed(() => updating.value || checkout.value.state === 'updating')

async function updateNow() {
  if (updating.value) return
  updating.value = true
  updated.value = null
  updateError.value = ''
  try {
    const { data } = await updateSkillSet(session.client, props.name)
    emit('state', data)
    updated.value = updateResult(data)
    announce.polite(visibleText(updated.value.text))
  } catch (e) {
    updateError.value = failureText(e, { lost: 'No answer from omo; the update may still be running. Look at the state above.', refused: 'The update was refused.' })
    announce.assertive(visibleText(updateError.value))
  } finally {
    updating.value = false
  }
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
      <Callout v-if="found" tone="warn" :title="SEARCH_TRUST_WARNING.title" data-skillset-found>
        <p>
          From skills.sh: <span class="skill-editor__path">{{ visibleText(found.name) }}</span> in
          <span class="skill-editor__path">{{ visibleText(found.source) }}</span>
          (<a :href="found.url" target="_blank" rel="noreferrer noopener" data-skillset-found-link>its page on skills.sh</a>).
        </p>
        <p v-for="(p, i) in SEARCH_TRUST_WARNING.paragraphs" :key="i">{{ p }}</p>
        <p data-skillset-found-folder>{{ visibleText(folderNote(found)) }} Choose the roles that get it; nothing is stored until you save.</p>
      </Callout>
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
      <p v-if="updated" class="skill-editor__result" :class="{ 'skill-editor__result--bad': !updated.ok }" data-skillset-result>
        {{ visibleText(updated.text) }}
      </p>
      <Callout v-if="updateError" tone="danger" data-skillset-update-error>
        <p>{{ visibleText(updateError) }}</p>
      </Callout>

      <FormSection
        legend="Who gets it"
        :level="4"
        description="Agents of the chosen roles are given the skills of this set: each skill's name, description and file, under the folder's path."
      >
        <div class="omo-field">
          <p :id="rolesId" class="omo-field__label">Roles</p>
          <div
            role="group"
            class="skill-editor__roles"
            :aria-labelledby="rolesId"
            :aria-describedby="[rolesHintId, rolesRefusal ? rolesErrorId : ''].filter(Boolean).join(' ')"
            data-skillset-roles
          >
            <Chip
              v-for="role in SKILL_ROLES"
              :key="role.id"
              as="button"
              :label="role.label"
              :pressed="draft.roles.includes(role.id)"
              :data-skillset-role="role.id"
              @click="flip(role.id)"
            />
          </div>
          <p :id="rolesHintId" class="omo-field__hint" data-skillset-roles-hint>
            {{ draft.roles.length === 0 ? 'None chosen: every role gets this set.' : 'Only the chosen roles get this set.' }}
          </p>
          <p v-if="rolesRefusal" :id="rolesErrorId" class="omo-field__error">{{ rolesRefusal }}</p>
        </div>
        <ListField
          v-model="draft.paths"
          label="Folders and files"
          item-label="Path"
          add-label="Add path"
          empty-text="None: the whole repository."
          placeholder="skills/grill-me"
          hint="Relative to the repository's root. A folder with a SKILL.md is one skill, a folder without one gives every skill below it, a single file is a skill of its own."
          :error="fe['paths']"
          :errors="pathErrors"
          data-skillset-paths
        />
        <Callout v-if="missing.length > 0" tone="warn" data-skillset-missing>
          <p>
            Not in the checkout: <span class="skill-editor__path">{{ missing.map(visibleText).join(', ') }}</span>. Agents are not given
            {{ missing.length === 1 ? 'it' : 'them' }} until the repository has {{ missing.length === 1 ? 'it' : 'them' }}.
          </p>
        </Callout>
      </FormSection>

      <FormSection
        legend="What agents are told"
        :level="4"
        description="A text of your own for each role that gets the set; it comes before the list of skills. Leave it empty to give only the skills."
      >
        <TextArea
          v-for="role in textRoles"
          :key="role.id"
          v-model="draft.instructions[role.id]"
          :label="role.label"
          :rows="2"
          :error="fe[`instructions.${role.id}`]"
          :data-skillset-text="role.id"
        />
      </FormSection>

      <Callout v-if="deleteError" tone="danger" data-delete-error>
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
.skill-editor__roles {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 1.5);
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
