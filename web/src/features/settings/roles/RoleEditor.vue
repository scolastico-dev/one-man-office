<script setup lang="ts">
// One role's models and assignment mode, as a form over its settings item
// (the page owns it, so one Save bar saves every role). The list is the
// order of use (the page says what a repeat means). A model no profile
// defines any more stays in the list, marked, until
// the user removes it; the server refuses a save that still names it and
// says which entry. A refusal, a message without a field and a change made
// elsewhere ("Use the new value" or "Keep mine") show here, under this role.
import { computed, ref, toRef, useId } from 'vue'
import { useRoute } from 'vue-router'
import type { Model } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import DurationField from '../../../components/ui/DurationField.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ReorderList from '../../../components/ui/ReorderList.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { settingsLocation } from '../../../locations'
import { roleTitle } from '../../onboarding/agentSetup'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { CONFLICT_TEXT, diskChangeText, type SettingsResource } from '../useSettingsResource'
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
import type { RoleDraft } from './roleResource'

const props = withDefaults(
  defineProps<{
    /** The role: `ceo`, `pm`, `developer` or `reviewer`. */
    role: string
    /** The role's settings item (roleResourceOptions), saved by the page. */
    resource: SettingsResource<RoleDraft>
    /** One line under the role's name. */
    description?: string
    /** Every model of every profile; null until read. */
    models: readonly Model[] | null
    /** False while the CEO is switched off. */
    enabled?: boolean
  }>(),
  { description: undefined, enabled: true },
)

const route = useRoute()
const addChoice = ref('')
const listErrorId = useId()
const r = toRef(props, 'resource')

const title = computed(() => roleTitle(props.role))
const lower = computed(() => (props.role === 'ceo' ? 'CEO' : title.value.toLowerCase()))
const known = computed(() => modelIndex(props.models as Model[] | null))

const draft = computed(() => r.value.draft.value)
const list = computed(() => draft.value?.models ?? [])
const items = computed(() => rows(list.value, known.value))
const options = computed(() => addOptions((props.models as Model[] | null) ?? []))
const selected = computed(() => (options.value.some((o) => o.value === addChoice.value) ? addChoice.value : (options.value[0]?.value ?? '')))
const modeOptions = computed(() => assignmentOptions(draft.value?.assignment ?? ''))
const meaning = computed(() => assignmentMeaning(draft.value?.assignment ?? ''))
const resetWeight = computed({
  get: () => draft.value?.super_smart_reset_weight ?? '',
  set: (value: string) => {
    if (draft.value === null) return
    if (value === '') delete draft.value.super_smart_reset_weight
    else draft.value.super_smart_reset_weight = value
  },
})

const modelsError = computed(() => {
  const found = Object.entries(r.value.fieldErrors.value).find(([field]) => field === 'models' || modelErrorIndex(field) >= 0)
  return found === undefined ? null : { message: found[1], index: modelErrorIndex(found[0]) }
})
const assignmentError = computed(() => r.value.fieldErrors.value['assignment'])
const unplaced = computed(() => unplacedErrors(r.value.fieldErrors.value, (f) => f === 'assignment' || f === 'models' || modelErrorIndex(f) >= 0 || (f === 'super_smart_reset_weight' && draft.value?.assignment === 'super_smart')))
const mark = (id: string) => markOf(id, known.value)
const noteOf = (id: string, note: string | undefined) =>
  mark(id) === 'missing' ? 'No profile defines this model. Remove it, or add the model to a profile on the Agents page.' : (note ?? '')

function change(models: string[]) {
  if (draft.value !== null) draft.value.models = models
}
function add() {
  if (selected.value !== '') change(addModel(list.value, selected.value))
}
</script>

<template>
  <div class="role-editor" :data-role-editor="role">
    <FileErrors :errors="resource.broken.value" />
    <Callout v-if="resource.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(resource.changedOnDisk.value) }}</p>
    </Callout>
    <FormSection :legend="title" :description="description">
      <p v-if="draft === null && resource.loading.value" class="role-editor__note" data-role-loading>Reading the {{ lower }}’s models…</p>
      <Callout v-else-if="draft === null" tone="danger" live="assertive" data-role-load-error>
        <p>{{ resource.error.value }}</p>
        <template #actions><UiButton size="sm" @click="resource.reload()">Try again</UiButton></template>
      </Callout>
      <template v-else-if="draft !== null">
        <Callout v-if="resource.conflict.value !== null" tone="warn" data-role-conflict>
          <p>{{ CONFLICT_TEXT }}</p>
          <template #actions>
            <UiButton size="sm" data-use-theirs @click="resource.useTheirs()">Use the new value</UiButton>
            <UiButton size="sm" data-keep-mine @click="resource.keepMine()">Keep mine</UiButton>
          </template>
        </Callout>
        <Callout v-if="resource.error.value" tone="danger" data-role-error>
          <p>{{ resource.error.value }}</p>
        </Callout>
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
        <DurationField
          v-if="draft.assignment === 'super_smart'"
          v-model="resetWeight"
          label="Reset weight"
          placeholder="24h"
          hint="Higher values favor capacity that resets soon more strongly. Leave empty for 24h; 0s removes the reset bonus."
          :examples="['6h', '24h', '168h']"
          :error="resource.fieldErrors.value['super_smart_reset_weight']"
          data-reset-weight
        />
      </template>
    </FormSection>
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
