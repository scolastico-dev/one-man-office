<script setup lang="ts">
// omo's general MCP switches (the `mcp` key of settings.yaml): whether omo
// serves its MCP endpoint, and per agent program whether the sessions omo
// starts get omo's MCP entry. A form of its own with its own Save bar; a
// change of other general settings is no conflict for it.
import { computed, ref, watch } from 'vue'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import FileErrors from '../FileErrors.vue'
import { useGeneralForm } from '../general/generalForms'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid } from '../useSettingsResource'

const emit = defineEmits<{
  /** Whether MCP is switched on, as stored (not as edited). */
  enabled: [boolean]
}>()

const form = useGeneralForm(['mcp'])
watch(
  () => form.value.value?.mcp.enabled,
  (on) => {
    if (on !== undefined) emit('enabled', on)
  },
  { immediate: true },
)
useLeaveGuard(form.dirty)
const draft = computed(() => form.draft.value)
const unplaced = computed(() => unplacedErrors(form.fieldErrors.value, []))
const root = ref<HTMLElement>()

async function save() {
  if ((await form.save()) === 'refused') await focusFirstInvalid(root.value)
}
</script>

<template>
  <div ref="root" data-mcp-switches>
    <FormSection
      legend="General switches"
      :level="3"
      description="omo's own MCP endpoint and the entry it gives the agent sessions it starts. Your own configuration of the agent programs is not changed by these."
    >
      <FileErrors :errors="form.broken.value" />
      <Callout v-if="form.changedOnDisk.value" tone="warn" live="polite">
        <p>{{ diskChangeText(form.changedOnDisk.value) }}</p>
      </Callout>
      <p v-if="form.loading.value && !draft" class="switches__note">Reading the settings…</p>
      <Callout v-else-if="!draft && form.error.value" tone="danger" live="assertive">
        <p>{{ visibleText(form.error.value) }}</p>
        <template #actions><UiButton size="sm" @click="form.reload()">Try again</UiButton></template>
      </Callout>
      <template v-if="draft">
        <UnplacedErrors :errors="unplaced" />
        <Toggle v-model="draft.mcp.enabled" label="Serve omo's MCP endpoint" data-mcp-enabled />
        <p class="switches__note">Off: omo answers no MCP client and gives its agents no MCP entry; the omo ai command line keeps working.</p>
        <Toggle v-model="draft.mcp.inject.claude" label="Claude Code sessions get omo's MCP entry" data-mcp-inject="claude" />
        <Toggle v-model="draft.mcp.inject.codex" label="Codex sessions get omo's MCP entry" data-mcp-inject="codex" />
        <p class="switches__note">Each session omo starts gets the entry for itself. Antigravity has no such option: register omo in its configuration under Agent programs.</p>
        <SaveBar
          label="Save changes to the general MCP switches"
          :dirty="form.dirty.value"
          :busy="form.saving.value"
          :status="form.status.value"
          :error="form.error.value ? visibleText(form.error.value) : undefined"
          @save="save"
          @discard="form.discard()"
        >
          <template v-if="form.status.value === 'conflict'">
            <UiButton size="sm" @click="form.useTheirs()">Use the new value</UiButton>
            <UiButton size="sm" @click="form.keepMine()">Keep mine</UiButton>
          </template>
        </SaveBar>
      </template>
    </FormSection>
  </div>
</template>

<style scoped>
.switches__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
