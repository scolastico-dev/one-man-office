<script setup lang="ts">
// The Limits section of the settings: how many agents run, when an agent or a
// question counts as stuck, how long a stop waits, how often folders are
// searched. The server checks every value and names the field it refuses.
import { computed } from 'vue'
import DurationField from '../../../components/ui/DurationField.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import NumberField from '../../../components/ui/NumberField.vue'
import { numberFieldOf, unplacedErrors, useGeneralForm } from '../general/generalForms'
import UnplacedErrors from '../general/UnplacedErrors.vue'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('limits')
const form = useGeneralForm(['max_running_agents', 'health', 'waiting', 'stop', 'rescan_interval'])
const draft = form.draft
const err = (field: string) => form.fieldErrors.value[field]
const FIELDS = [
  'max_running_agents',
  'health.silent_after',
  'health.question_unanswered_after',
  'waiting.timeouts_before_investigate',
  'stop.handoff_timeout',
  'rescan_interval',
]
const unplaced = computed(() => unplacedErrors(form, FIELDS))

const running = numberFieldOf(
  form,
  'max_running_agents',
  () => draft.value?.max_running_agents ?? null,
  (n) => {
    if (draft.value) draft.value.max_running_agents = n
  },
)
const timeouts = numberFieldOf(
  form,
  'waiting.timeouts_before_investigate',
  () => draft.value?.waiting.timeouts_before_investigate ?? null,
  (n) => {
    if (draft.value) draft.value.waiting.timeouts_before_investigate = n
  },
)
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" :resource="form">
    <div v-if="draft" class="limits">
      <UnplacedErrors :errors="unplaced" />
      <FormSection legend="Agents" description="Applies at the next scheduling pass.">
        <NumberField
          v-model="running.model.value"
          label="Max running agents"
          :min="1"
          :max="64"
          :error="running.error.value"
          data-field="max_running_agents"
          hint="Agents waiting in omo ai wait hold no slot; the CEO never counts. A story can limit its own parallel tasks in its panel."
          @parse="running.onParse"
        />
      </FormSection>
      <FormSection legend="Health" description="Applies at the next scheduling pass.">
        <DurationField
          v-model="draft.health.silent_after"
          label="Silent after"
          hint="An agent without output this long is nudged."
          :error="err('health.silent_after')"
          data-field="health.silent_after"
        />
        <DurationField
          v-model="draft.health.question_unanswered_after"
          label="Question unanswered after"
          hint="A question to you this old rings the bell again."
          :error="err('health.question_unanswered_after')"
          data-field="health.question_unanswered_after"
        />
        <NumberField
          v-model="timeouts.model.value"
          label="Timeouts before investigating"
          :min="1"
          :max="100"
          :error="timeouts.error.value"
          data-field="waiting.timeouts_before_investigate"
          hint="Waits that ran out before the product manager looks into it."
          @parse="timeouts.onParse"
        />
      </FormSection>
      <FormSection legend="Stop and scan">
        <DurationField
          v-model="draft.stop.handoff_timeout"
          label="Handoff time limit at stop"
          hint="How long omo stop waits for handoff comments."
          :error="err('stop.handoff_timeout')"
          data-field="stop.handoff_timeout"
        />
        <DurationField
          v-model="draft.rescan_interval"
          label="Rescan interval"
          hint="How often the workspace folders are searched."
          :error="err('rescan_interval')"
          data-field="rescan_interval"
        />
      </FormSection>
    </div>
  </SettingsPage>
</template>

<style scoped>
.limits {
  display: grid;
  gap: calc(var(--omo-space) * 6);
}
</style>
