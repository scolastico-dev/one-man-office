<script setup lang="ts">
// One role's models and assignment mode, as its own form: loaded with its
// revision, saved with If-Match, kept up to date while the page is open. The
// list is the order of use; a model listed twice is picked twice as often by
// the random mode. A model no profile defines any more stays in the list,
// marked, until the user removes it; the server refuses a save that still
// names it and says which entry.
import { computed, ref, useId } from 'vue'
import { useRoute } from 'vue-router'
import { getRoles, getSettings, putRole } from '../../../api/endpoints'
import { NO_REVISION, type ApiEvent, type FileError, type Model, type RoleSettings, type SettingsChangedData, type SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ReorderList from '../../../components/ui/ReorderList.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { settingsLocation } from '../../../locations'
import { useSession } from '../../../session'
import { roleTitle } from '../../onboarding/agentSetup'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import {
  MARK_TEXT,
  addModel,
  addOptions,
  assignmentMeaning,
  assignmentOptions,
  markOf,
  modelErrorIndex,
  modelIndex,
  moveModel,
  removeModel,
  rows,
  weightText,
} from './roleForm'

const props = withDefaults(
  defineProps<{
    /** The role: `ceo`, `pm`, `developer` or `reviewer`. */
    role: string
    /** One line under the role's name. */
    description?: string
    /** Every model of every profile; null until read. */
    models: readonly Model[] | null
    /** False while the CEO is switched off. */
    enabled?: boolean
  }>(),
  { description: undefined, enabled: true },
)

type RoleDraft = Pick<RoleSettings, 'name' | 'models' | 'assignment'>

const { client } = useSession()
const route = useRoute()
const root = ref<HTMLElement>()
const addChoice = ref('')
const listErrorId = useId()

const title = computed(() => roleTitle(props.role))
const lower = computed(() => (props.role === 'ceo' ? 'CEO' : title.value.toLowerCase()))
const known = computed(() => modelIndex(props.models as Model[] | null))

const fileOf = (file: string) => new RegExp(`[\\\\/]roles[\\\\/]${props.role}\\.ya?ml$`).test(file)
const plain = (r: RoleSettings): RoleDraft => ({ name: r.name, models: [...r.models], assignment: r.assignment })

const r = useSettingsResource<RoleDraft>({
  load: async () => {
    const [roles, settings] = await Promise.all([getRoles(client), getSettings(client)])
    const found = roles.data.find((x) => x.name === props.role)
    const value = found === undefined ? { name: props.role, models: [], assignment: 'round_robin' } : plain(found)
    const errors: FileError[] = (settings.data.errors ?? []).filter((e) => fileOf(e.file))
    return { value, revision: found?.revision ?? NO_REVISION, errors }
  },
  save: async (draft, revision) => {
    const { data, eventId } = await putRole(client, { ...draft, models: [...draft.models] }, revision)
    return { value: plain(data), revision: data.revision ?? NO_REVISION, eventId }
  },
  watches: (ev: ApiEvent) => {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'roles' && d.name === props.role
    }
    if (ev.kind === 'settings.invalid') return fileOf(String((ev.data as Partial<SettingsInvalidData> | null)?.file ?? ''))
    return false
  },
})
useLeaveGuard(r.dirty)

const draft = computed(() => r.draft.value)
const list = computed(() => draft.value?.models ?? [])
const items = computed(() => rows(list.value, known.value))
const options = computed(() => addOptions((props.models as Model[] | null) ?? []))
const selected = computed(() => (options.value.some((o) => o.value === addChoice.value) ? addChoice.value : (options.value[0]?.value ?? '')))
const modeOptions = computed(() => assignmentOptions(draft.value?.assignment ?? ''))
const meaning = computed(() => assignmentMeaning(draft.value?.assignment ?? ''))

const modelsError = computed(() => {
  const found = Object.entries(r.fieldErrors.value).find(([field]) => field === 'models' || modelErrorIndex(field) >= 0)
  return found === undefined ? null : { message: found[1], index: modelErrorIndex(found[0]) }
})
const assignmentError = computed(() => r.fieldErrors.value['assignment'])
const unplaced = computed(() => unplacedErrors(r.fieldErrors.value, (f) => f === 'assignment' || f === 'models' || modelErrorIndex(f) >= 0))
const mark = (id: string) => markOf(id, known.value)
const noteOf = (id: string, note: string | undefined) =>
  mark(id) === 'missing' ? 'No profile defines this model. Remove it, or add the model to a profile on the Agents page.' : (note ?? '')

function change(models: string[]) {
  if (draft.value !== null) draft.value.models = models
}
function add() {
  if (selected.value !== '') change(addModel(list.value, selected.value))
}
async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}
</script>

<template>
  <div ref="root" class="role-editor" :data-role-editor="role">
    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <FormSection :legend="title" :description="description">
      <p v-if="draft === null && r.loading.value" class="role-editor__note" data-role-loading>Reading the {{ lower }}’s models…</p>
      <Callout v-else-if="draft === null" tone="danger" live="assertive" data-role-load-error>
        <p>{{ r.error.value }}</p>
        <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
      </Callout>
      <template v-else-if="draft !== null">
        <UnplacedErrors :errors="unplaced" />
        <Callout v-if="!enabled" tone="info" data-role-off>
          <p>
            The CEO is switched off; it needs no model.
            <RouterLink :to="settingsLocation('general', route)">Switch it on under General</RouterLink>.
          </p>
        </Callout>
        <div class="role-editor__list">
          <ReorderList
            :items="items"
            :label="`Models of the ${lower}`"
            empty-text="No models yet. Nothing starts for this role until it has one."
            :aria-invalid="modelsError ? 'true' : undefined"
            :aria-describedby="modelsError ? listErrorId : undefined"
            @move="(from, to) => change(moveModel(list, from, to))"
            @remove="(i) => change(removeModel(list, i))"
          >
            <template #item="{ item, index }">
              <span class="role-model" :data-model="item.id" :data-mark="mark(item.id)">
                {{ visibleText(item.id)
                }}<span v-if="weightText(list, item.id)" class="role-model__weight"> {{ weightText(list, item.id) }}</span>
                <StatusBadge v-if="mark(item.id) !== 'ok'" :tone="'danger'" :text="MARK_TEXT[mark(item.id) as 'missing']" />
                <StatusBadge v-if="modelsError && modelsError.index === index" tone="danger" text="refused" />
              </span>
              <span v-if="noteOf(item.id, item.note)" class="role-model__note">{{ visibleText(noteOf(item.id, item.note)) }}</span>
            </template>
          </ReorderList>
          <p v-if="modelsError" :id="listErrorId" class="omo-field__error" data-field-error="models">{{ modelsError.message }}</p>
        </div>

        <div class="role-editor__add">
          <SelectField label="Add model" :options="options" :model-value="selected" @update:model-value="addChoice = $event" />
          <UiButton :disabled="options.length === 0" :aria-label="`Add model to the ${lower}`" data-add-model @click="add">Add</UiButton>
        </div>
        <p v-if="props.models !== null && options.length === 0" class="role-editor__note">
          No model exists yet.
          <RouterLink :to="settingsLocation('agents', route)">Define a profile with models on the Agents page</RouterLink>.
        </p>

        <SelectField
          :model-value="draft.assignment"
          label="Assignment"
          :options="modeOptions"
          :hint="meaning"
          :error="assignmentError"
          data-assignment
          @update:model-value="draft.assignment = $event"
        />
        <Callout v-if="draft.assignment === 'random'" tone="info"><p>A model listed twice is picked twice as often.</p></Callout>
      </template>
    </FormSection>
    <SaveBar
      v-if="draft !== null"
      :label="`Save changes to the ${lower}`"
      :dirty="r.dirty.value"
      :busy="r.saving.value"
      :status="r.status.value"
      :error="r.error.value || undefined"
      @save="save"
      @discard="r.discard()"
    >
      <template v-if="r.status.value === 'conflict'">
        <UiButton size="sm" data-use-theirs @click="r.useTheirs()">Use the new value</UiButton>
        <UiButton size="sm" data-keep-mine @click="r.keepMine()">Keep mine</UiButton>
      </template>
    </SaveBar>
  </div>
</template>

<style scoped>
.role-editor {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.role-editor__list {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
}
.role-editor__add {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the input below a one-line label. */
.role-editor__add :deep(.omo-field + *),
.role-editor__add > button {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
}
.role-editor__note {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  color: var(--omo-muted);
}
.role-model {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.role-model__weight {
  color: var(--omo-accent);
}
.role-model__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
</style>
