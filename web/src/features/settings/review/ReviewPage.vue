<script setup lang="ts">
// The Review section of the settings: what review and what happens after
// approval a new ticket gets while it sets no value of its own, and who
// releases tasks.
import { computed } from 'vue'
import type { Review, WhenApproved } from '../../../api/types'
import FormSection from '../../../components/ui/FormSection.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import { REVIEW_LABEL, WHEN_LABEL } from '../../ticket/fieldRules'
import { useGeneralForm } from '../general/generalForms'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('review')
const form = useGeneralForm(['review_defaults', 'when_approved_defaults', 'tasks'])
const draft = form.draft
const err = (field: string) => form.fieldErrors.value[field]
const FIELDS = ['review_defaults.story', 'review_defaults.task', 'when_approved_defaults.story', 'when_approved_defaults.task', 'tasks.require_user_release']
const unplaced = computed(() => unplacedErrors(form.fieldErrors.value, FIELDS))

const REVIEW_OPTIONS = (Object.keys(REVIEW_LABEL) as Review[]).map((value) => ({
  value,
  label: REVIEW_LABEL[value],
}))
const APPROVED_OPTIONS = (Object.keys(WHEN_LABEL) as WhenApproved[]).map((value) => ({ value, label: WHEN_LABEL[value] }))
const RELEASE_OPTIONS = [
  { value: 'agents', label: 'The product manager or the CEO' },
  { value: 'user', label: 'You' },
]

/** No agent runs the full test suite for a story whose review is Skipped or Human. */
const STORY_NOTE = 'No agent runs the full test suite for stories.'
const storyNote = computed(() => (draft.value?.review_defaults.story === 'skipped' || draft.value?.review_defaults.story === 'human' ? STORY_NOTE : undefined))

const release = computed({
  get: () => (draft.value?.tasks.require_user_release ? 'user' : 'agents'),
  set(v: string) {
    if (draft.value) draft.value.tasks.require_user_release = v === 'user'
  },
})
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" :resource="form">
    <div v-if="draft" class="review">
      <UnplacedErrors :errors="unplaced" />
      <FormSection legend="Stories" description="Apply to tickets that set no value of their own.">
        <SelectField
          :model-value="draft.review_defaults.story"
          label="Review"
          :options="REVIEW_OPTIONS"
          :hint="storyNote"
          :error="err('review_defaults.story')"
          data-field="review_defaults.story"
          @update:model-value="(v) => (draft!.review_defaults.story = v as Review)"
        />
        <SelectField
          :model-value="draft.when_approved_defaults.story"
          label="When approved"
          :options="APPROVED_OPTIONS"
          :error="err('when_approved_defaults.story')"
          data-field="when_approved_defaults.story"
          @update:model-value="(v) => (draft!.when_approved_defaults.story = v as WhenApproved)"
        />
      </FormSection>
      <FormSection legend="Tasks" description="Apply to tickets that set no value of their own.">
        <SelectField
          :model-value="draft.review_defaults.task"
          label="Review"
          :options="REVIEW_OPTIONS"
          :error="err('review_defaults.task')"
          data-field="review_defaults.task"
          @update:model-value="(v) => (draft!.review_defaults.task = v as Review)"
        />
        <SelectField
          :model-value="draft.when_approved_defaults.task"
          label="When approved"
          :options="APPROVED_OPTIONS"
          hint="Tasks inside a story always merge into the story's branch."
          :error="err('when_approved_defaults.task')"
          data-field="when_approved_defaults.task"
          @update:model-value="(v) => (draft!.when_approved_defaults.task = v as WhenApproved)"
        />
        <SelectField
          v-model="release"
          label="Tasks are released by"
          :options="RELEASE_OPTIONS"
          :error="err('tasks.require_user_release')"
          data-field="tasks.require_user_release"
        />
      </FormSection>
    </div>
  </SettingsPage>
</template>

<style scoped>
.review {
  display: grid;
  gap: calc(var(--omo-space) * 6);
}
</style>
