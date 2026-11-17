<script setup lang="ts">
// "Additional plugin lists": the addresses (http or https) of plugin list
// files in the format of omo's own list, kept in settings.yaml
// (`plugin_lists`) and saved with this form's own Save bar. The server
// checks every address and names the row it refuses; after a save the
// plugin list above is read again, so the new lists show at once.
import { computed, ref } from 'vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { rowErrors } from '../agents/profileForm'
import { useGeneralForm } from '../general/generalForms'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { focusFirstInvalid } from '../useSettingsResource'
import { useLeaveGuard } from '../useLeaveGuard'
import { listWarning } from './pluginTrust'

const emit = defineEmits<{ saved: [] }>()

const form = useGeneralForm(['plugin_lists'])
useLeaveGuard(form.dirty)
const ready = computed(() => form.draft.value !== null)
const failed = computed(() => form.error.value)
/** The rows of the form. */
const addresses = computed({
  get: () => form.draft.value?.plugin_lists ?? [],
  set: (v: string[]) => {
    if (form.draft.value) form.draft.value.plugin_lists = v
  },
})
const fe = computed(() => form.fieldErrors.value)
const errors = computed(() => rowErrors(fe.value, 'plugin_lists'))
const unplaced = computed(() => unplacedErrors(fe.value, (f) => f === 'plugin_lists' || /^plugin_lists\.\d+$/.test(f)))
const root = ref<HTMLElement>()

async function save() {
  const outcome = await form.save()
  if (outcome === 'refused') await focusFirstInvalid(root.value)
  if (outcome === 'saved') emit('saved')
}
</script>

<template>
  <div ref="root">
    <FormSection legend="Additional plugin lists" :description="listWarning()" data-plugin-lists>
      <template v-if="ready">
        <UnplacedErrors :errors="unplaced" />
        <ListField
          v-model="addresses"
          label="List addresses"
          item-label="Address"
          add-label="Add list"
          empty-text="None: only omo's own list."
          placeholder="https://raw.githubusercontent.com/someone/omo-plugins/main/index.yaml"
          hint="An http:// or https:// address of a plugin list file in the format of omo's own list, such as a raw.githubusercontent.com address."
          :error="fe['plugin_lists']"
          :errors="errors"
          data-plugin-lists-field
        />
        <SaveBar
          label="Save changes to the plugin lists"
          :dirty="form.dirty.value"
          :busy="form.saving.value"
          :status="form.status.value"
          :error="form.error.value ? visibleText(form.error.value) : undefined"
          @save="save"
          @discard="form.discard()"
        >
          <template v-if="form.status.value === 'conflict'">
            <UiButton size="sm" data-use-theirs @click="form.useTheirs()">Use the new value</UiButton>
            <UiButton size="sm" data-keep-mine @click="form.keepMine()">Keep mine</UiButton>
          </template>
        </SaveBar>
      </template>
      <p v-else-if="failed" class="lists__note" data-plugin-lists-error>{{ visibleText(failed) }}</p>
      <p v-else class="lists__note">Reading the settings…</p>
    </FormSection>
  </div>
</template>

<style scoped>
.lists__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
