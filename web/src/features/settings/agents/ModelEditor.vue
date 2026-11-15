<script setup lang="ts">
// One model of a profile: what it adds to the profile's command and
// environment, whether agents may pick it, and its one-line description. A
// model's name is part of its id (`<profile>/<model>`, what roles and tickets
// store), so it can be typed only before the model is saved. Whether a model
// may be removed is the server's call (a role or an open ticket may use it).
import { computed } from 'vue'
import { HIDDEN_VALUE } from '../../../api/types'
import EnvTable from '../../../components/ui/EnvTable.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { errorsUnder, rowErrors, type ModelForm } from './profileForm'

const props = defineProps<{
  /** The profile the model belongs to, for its id. */
  profile: string
  /** The server's refusals by dotted field, for the whole profile. */
  fieldErrors: Readonly<Record<string, string>>
  /** Another model of the form has the same name. */
  nameTaken?: boolean
}>()
const emit = defineEmits<{ remove: [] }>()
const model = defineModel<ModelForm>({ required: true })

const shownName = computed(() => visibleText(model.value.name.trim()) || 'without a name')
const prefix = computed(() => `models.${model.value.name.trim()}`)
const err = (key: string) => props.fieldErrors[`${prefix.value}.${key}`]
const nameHint = computed(() => `Fixed once saved: roles and tickets name it ${visibleText(props.profile)}/${visibleText(model.value.name.trim()) || '<name>'}.`)
</script>

<template>
  <FormSection :legend="`Model ${shownName}`" :level="3" :data-model-editor="model.name.trim()">
    <TextField
      v-if="model.isNew"
      v-model="model.name"
      label="Model name"
      :hint="nameHint"
      :error="nameTaken ? 'Another model of this profile has this name; only one of them is kept.' : undefined"
      spellcheck="false"
      autocomplete="off"
      autocapitalize="off"
      data-model-name
    />
    <ListField
      v-model="model.args"
      label="Extra arguments"
      item-label="Argument"
      add-label="Add argument"
      empty-text="No extra arguments."
      hint="Added after the profile's base arguments."
      :error="err('args')"
      :errors="rowErrors(fieldErrors, `${prefix}.args`)"
    />
    <EnvTable
      v-model="model.env"
      label="Extra environment"
      :hidden="HIDDEN_VALUE"
      :errors="errorsUnder(fieldErrors, `${prefix}.env`)"
      :error="err('env')"
      empty-text="No extra variables."
      hint="Overrides the profile's environment for this model."
    />
    <Toggle v-model="model.selectable" label="Agents may choose it for tickets they create" />
    <TextField v-model="model.description" label="Description" hint="One line, shown where models are chosen." :error="err('description')" />
    <div>
      <UiButton size="sm" variant="danger" data-remove-model @click="emit('remove')">Remove model {{ shownName }}</UiButton>
    </div>
  </FormSection>
</template>
