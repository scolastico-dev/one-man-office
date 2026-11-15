<script setup lang="ts">
// The form of one agent profile: the program, its command, base arguments,
// environment and account folder, the start-prompt delivery under
// "Advanced", the usage script that reads how full its account is, and its
// models (each with the usage windows that count for it). A profile runs as the user, so it is edited
// here and nowhere else in the browser. The form is read again when its file
// changes; unsaved edits stay and the change is offered. A new profile is
// saved with If-Match "0", so it never replaces one that appeared under the
// same name meanwhile.
import { computed, onMounted, onScopeDispose, ref, useId, watch } from 'vue'
import { deleteProfile, getProfiles, getSettings, listUsageScripts, putProfile } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import { NO_REVISION, type ApiEvent, type Profile, type SettingsChangedData, type SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import DurationField from '../../../components/ui/DurationField.vue'
import EnvTable from '../../../components/ui/EnvTable.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
import NumberField from '../../../components/ui/NumberField.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import FolderPickerDialog from '../projects/FolderPickerDialog.vue'
import { useFolderPicker } from '../projects/useFolderPicker'
import { askFirst, useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import ModelEditor from './ModelEditor.vue'
import {
  accountVariable,
  blankForm,
  CLI_OPTIONS,
  errorsOfProfile,
  errorsUnder,
  fileOfProfile,
  newModel,
  rowErrors,
  toForm,
  toProfile,
  unplacedErrors,
  type ModelForm,
  type ProfileForm,
} from './profileForm'

const props = withDefaults(
  defineProps<{
    name: string
    /** For a profile not stored yet: the program chosen for it. */
    newCli?: string | null
  }>(),
  { newCli: null },
)
const emit = defineEmits<{
  /** Whether the form has unsaved edits. */
  dirty: [boolean]
  /** The profile was deleted. */
  deleted: []
  /** Duplicate… was chosen: the stored profile. */
  duplicate: [Profile]
}>()

const session = useSession()
const announce = useAnnouncer()

/** The stored profile as last read, for Duplicate…. */
const stored = ref<Profile | null>(null)
/** The profile was stored at some point while the form is open; its file is gone now. */
let wasStored = false
const gone = ref(false)
const goneText = computed(() => `The profile ${visibleText(props.name)} no longer exists.`)

const r = useSettingsResource<ProfileForm>({
  async load() {
    const { data } = await getProfiles(session.client)
    const errors = errorsOfProfile(data.errors, props.name)
    const p = data.profiles.find((x) => x.name === props.name)
    stored.value = p ?? null
    if (p !== undefined) {
      wasStored = true
      gone.value = false
      return { value: toForm(p), revision: p.revision ?? NO_REVISION, errors }
    }
    // A new profile starts from a blank form, unsaved until it is saved; one
    // that was stored and is gone now (deleted by hand) says so instead.
    if (props.newCli !== null && !wasStored) return { value: blankForm(props.name, props.newCli), revision: NO_REVISION, errors, unsaved: true }
    gone.value = true
    throw new Error(goneText.value)
  },
  async save(draft, revision) {
    const { data, eventId } = await putProfile(session.client, toProfile(draft), revision)
    stored.value = data
    wasStored = true
    gone.value = false
    return { value: toForm(data), revision: data.revision ?? NO_REVISION, eventId }
  },
  watches(ev: ApiEvent) {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'profiles' && d.name === props.name
    }
    if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      return typeof d?.file === 'string' && fileOfProfile(d.file) === props.name
    }
    return false
  },
})

useLeaveGuard(r.dirty)
watch(r.dirty, (d) => emit('dirty', d), { immediate: true })

const ids = useId()
const titleId = `${ids}-title`
const modelsErrorId = `${ids}-models-error`
const root = ref<HTMLElement>()
const heading = ref<HTMLElement>()
onMounted(() => heading.value?.focus())

const draft = computed(() => r.draft.value)
const fe = computed(() => r.fieldErrors.value)
const isStored = computed(() => stored.value !== null)
const shownName = computed(() => visibleText(props.name))

// The usage scripts to choose from, and how often omo runs them. Read when
// the form opens and again when a script or the general settings change; a
// failed read leaves the stored choice selectable.
const store = useBoard()
const scriptNames = ref<string[]>([])
const refreshEvery = ref('')
async function readScripts() {
  try {
    const [{ data: list }, { data: general }] = await Promise.all([listUsageScripts(session.client), getSettings(session.client)])
    scriptNames.value = list.map((x) => x.name)
    refreshEvery.value = general.settings.usage?.refresh_interval ?? ''
  } catch {
    // The select still offers the stored script and none.
  }
}
void readScripts()
const offScripts = store.onEvent('settings.changed', (ev) => {
  const section = (ev.data as Partial<SettingsChangedData> | null)?.section
  if (section === 'usage' || section === 'general') void readScripts()
})
onScopeDispose(offScripts)
const scriptOptions = computed(() => {
  const chosen = draft.value?.usage_script ?? ''
  const names = [...scriptNames.value]
  if (chosen !== '' && !names.includes(chosen)) names.push(chosen)
  return [{ value: '', label: 'None: no usage readings' }, ...names.map((n) => ({ value: n, label: visibleText(n) }))]
})
const scriptHint = computed(() => {
  if ((draft.value?.usage_script ?? '') === '') return "Without a script this profile's models count as unmetered: omo never keeps agents off them."
  return `The script runs as you with this profile's account and environment.${refreshEvery.value ? ` omo runs it every ${refreshEvery.value}.` : ''}`
})

const variable = computed(() => accountVariable(draft.value?.cli ?? ''))
const accountHint = computed(() =>
  variable.value === '' ? 'A custom program gets no variable for it: set its own in the environment.' : `Becomes ${variable.value} for this profile's agents.`,
)
const argsHint = computed(() =>
  draft.value?.cli === 'custom' ? '%prompt% in the arguments is replaced by the start prompt.' : 'Every model of this profile starts with these.',
)
const INJECT_OPTIONS = [
  { value: 'default', label: "The program's default (on)" },
  { value: 'on', label: 'On: as a launch argument, or typed into the terminal' },
  { value: 'off', label: 'Off: only through %prompt% in the arguments' },
]

const unplaced = computed(() => unplacedErrors(fe.value, (draft.value?.models ?? []).map((m) => m.name.trim())))

const keys = new WeakMap<object, number>()
let nextKey = 0
function keyOf(m: ModelForm): number {
  let k = keys.get(m)
  if (k === undefined) {
    k = nextKey++
    keys.set(m, k)
  }
  return k
}
function nameTaken(m: ModelForm): boolean {
  const name = m.name.trim()
  return name !== '' && (draft.value?.models ?? []).filter((x) => x.name.trim() === name).length > 1
}

function addModel() {
  draft.value?.models.push(newModel())
  announce.polite('Model added at the end.')
}
function removeModel(i: number) {
  const m = draft.value?.models.splice(i, 1)[0]
  if (m) announce.polite(`Model ${m.name.trim() || 'without a name'} removed. Save to apply.`)
}

const picker = useFolderPicker()
async function browseAccount() {
  const start = draft.value?.account_dir ?? ''
  const path = await picker.choose({ title: 'Choose the account folder', start })
  if (path !== null && draft.value) draft.value.account_dir = path
}

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}

const deleteError = ref('')
const deleting = ref(false)
async function remove() {
  const ok = await askFirst({
    title: `Delete the profile ${shownName.value}?`,
    text: 'omo removes its file. Agents already running keep what they started with.',
    confirm: 'Delete',
    cancel: 'Keep it',
  })
  if (!ok) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deleteProfile(session.client, props.name, r.revision.value ?? NO_REVISION)
    announce.polite(`Profile ${shownName.value} deleted.`)
    emit('deleted')
  } catch (e) {
    deleteError.value = failureText(e, {
      lost: 'No answer from omo, so the profile may have been deleted. Look in the profile list.',
      refused: 'The profile could not be deleted.',
    })
    announce.assertive(deleteError.value)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section ref="root" class="profile-editor" :aria-labelledby="titleId" :data-profile-editor="name">
    <div class="profile-editor__head">
      <h2 :id="titleId" ref="heading" class="profile-editor__title" tabindex="-1">
        Profile {{ shownName }}<span v-if="!isStored && !r.loading.value && !gone" class="profile-editor__new"> (not saved yet)</span>
      </h2>
      <div v-if="isStored" class="profile-editor__actions">
        <UiButton size="sm" :disabled="stored === null" data-duplicate @click="stored && emit('duplicate', stored)">Duplicate…</UiButton>
        <UiButton size="sm" variant="danger" :disabled="deleting" data-delete-profile @click="remove">Delete…</UiButton>
      </div>
    </div>

    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <Callout v-if="deleteError" tone="danger" live="assertive" data-delete-error>
      <p>{{ deleteError }}</p>
    </Callout>
    <Callout v-if="gone" tone="warn" live="polite" data-profile-gone>
      <p>{{ goneText }}</p>
    </Callout>
    <p v-if="r.loading.value && !draft" class="profile-editor__note">Reading the profile…</p>
    <Callout v-else-if="!draft && r.error.value && !gone" tone="danger" live="assertive" data-settings-load-error>
      <p>{{ r.error.value }}</p>
      <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
    </Callout>

    <template v-if="draft && !(gone && !r.dirty.value)">
      <UnplacedErrors :errors="unplaced" />

      <FormSection legend="Program" :level="3" description="Applies to agents started after the save; running agents keep what they started with.">
        <TextField :model-value="name" label="Name" disabled hint="Fixed after creation. Duplicate… makes a copy under a new name." />
        <SelectField v-model="draft.cli" label="Agent program" :options="[...CLI_OPTIONS]" :error="fe['cli']" />
        <TextField v-model="draft.command" label="Command" :error="fe['command']" spellcheck="false" autocomplete="off" autocapitalize="off" data-command />
        <ListField
          v-model="draft.args"
          label="Base arguments"
          item-label="Argument"
          add-label="Add argument"
          empty-text="No arguments."
          :hint="argsHint"
          :error="fe['args']"
          :errors="rowErrors(fe, 'args')"
        />
        <EnvTable
          v-model="draft.env"
          label="Environment"
          :errors="errorsUnder(fe, 'env')"
          :error="fe['env']"
          empty-text="No variables."
          hint="Overrides the agent environment. Values are used exactly as typed: $VAR and `command` are neither expanded nor run here, only in the agent environment on the Environment page."
        />
        <div class="profile-editor__path">
          <TextField
            v-model="draft.account_dir"
            label="Account folder"
            :hint="accountHint"
            :error="fe['account_dir']"
            spellcheck="false"
            autocomplete="off"
            autocapitalize="off"
            data-account-dir
          />
          <UiButton class="profile-editor__browse" aria-label="Browse… for the account folder" data-account-browse @click="browseAccount">Browse…</UiButton>
        </div>
        <details class="profile-editor__advanced" :open="Boolean(fe['prompt_delivery.inject'] || fe['prompt_delivery.delay'] || fe['prompt_delivery.retries'] || fe['prompt_delivery.retry_wait']) || undefined">
          <summary>Advanced: start-prompt delivery</summary>
          <div class="profile-editor__advanced-body">
            <p class="profile-editor__note">Leave a field empty for the program's default: on, 3s delay, 3 retries every 30s.</p>
            <SelectField v-model="draft.inject" label="Deliver the start prompt" :options="INJECT_OPTIONS" :error="fe['prompt_delivery.inject']" />
            <DurationField v-model="draft.delay" label="Delay before typing it" :error="fe['prompt_delivery.delay']" :examples="['3s', '10s']" />
            <NumberField v-model="draft.retries" label="Retries" :min="0" :error="fe['prompt_delivery.retries']" hint="Resends while the agent shows no output." />
            <DurationField v-model="draft.retry_wait" label="Wait between retries" :error="fe['prompt_delivery.retry_wait']" :examples="['30s', '1m']" />
          </div>
        </details>
      </FormSection>

      <FormSection legend="Usage" :level="3" description="How full this profile's account is decides which of its models agents may start on.">
        <SelectField
          v-model="draft.usage_script"
          label="Usage script"
          :options="scriptOptions"
          :hint="scriptHint"
          :error="fe['usage_script']"
          data-usage-script
        />
      </FormSection>

      <ModelEditor
        v-for="(m, i) in draft.models"
        :key="keyOf(m)"
        v-model="draft.models[i]!"
        :profile="name"
        :field-errors="fe"
        :name-taken="nameTaken(m)"
        @remove="removeModel(i)"
      />
      <div class="profile-editor__add">
        <p v-if="draft.models.length === 0" class="profile-editor__note">No model. A profile needs at least one.</p>
        <p v-if="fe['models']" :id="modelsErrorId" class="omo-field__error" data-models-error>{{ fe['models'] }}</p>
        <div>
          <UiButton :aria-describedby="fe['models'] ? modelsErrorId : undefined" data-add-model @click="addModel">Add model</UiButton>
        </div>
      </div>

      <FolderPickerDialog v-bind="picker.dialog.value" @choose="picker.chosen" @close="picker.closed" />

      <SaveBar :dirty="r.dirty.value" :busy="r.saving.value" :status="r.status.value" :error="r.error.value || undefined" @save="save" @discard="r.discard()">
        <template v-if="r.status.value === 'conflict'">
          <UiButton size="sm" data-use-theirs @click="r.useTheirs()">Use the new value</UiButton>
          <UiButton size="sm" data-keep-mine @click="r.keepMine()">Keep mine</UiButton>
        </template>
      </SaveBar>
    </template>
  </section>
</template>

<style scoped>
.profile-editor {
  display: grid;
  gap: calc(var(--omo-space) * 6);
  min-width: 0;
}
.profile-editor__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
}
.profile-editor__title {
  margin: 0;
  font-size: var(--omo-text-lg);
  line-height: var(--omo-text-lg-lh);
  overflow-wrap: anywhere;
}
.profile-editor__new,
.profile-editor__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.profile-editor__note {
  margin: 0;
}
.profile-editor__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.profile-editor__path {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the input below a one-line label, as Browse… of the workspace folders. */
.profile-editor__browse {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
  min-height: calc(var(--omo-space) * 10);
}
.profile-editor__advanced > summary {
  cursor: pointer;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.profile-editor__advanced-body {
  display: grid;
  gap: calc(var(--omo-space) * 5);
  padding-top: calc(var(--omo-space) * 4);
}
.profile-editor__add {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
</style>
