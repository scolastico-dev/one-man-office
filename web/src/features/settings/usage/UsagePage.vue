<script setup lang="ts">
// The Usage section of the settings: the limits that keep agents off busy
// models, and the usage scripts that tell omo how full each account is. A
// script is code that runs as the user with the user's credentials. The
// limits and every script are forms of their own, each with its own Save
// bar; one script is open at a time, and leaving one with unsaved changes
// asks first. The script list is read again when a script or a profile
// changes, after a snapshot and when the event stream is live again; reads
// are numbered so an older answer never replaces a newer one.
import { computed, ref, useId } from 'vue'
import { listUsageScripts } from '../../../api/endpoints'
import type { ApiEvent, SettingsChangedData, SettingsInvalidData, UsageScriptInfo } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { useGeneralForm } from '../general/generalForms'
import { liveRead } from '../../../store/liveRead'
import NameDialog from '../NameDialog.vue'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { focusAfterClose, rowButton, useOpenEditor } from '../openEditor'
import ScriptEditor from './ScriptEditor.vue'
import ScriptList from './ScriptList.vue'
import UsageThresholds from './UsageThresholds.vue'
import { fileOfScript } from './usageForms'

const def = sectionDef('usage')
const session = useSession()
const store = useBoard()
const limits = useGeneralForm(['usage'])

const scripts = ref<UsageScriptInfo[]>([])
const loaded = ref(false)
const loadError = ref('')

function concernsScripts(ev: ApiEvent): boolean {
  if (ev.kind === 'settings.changed') {
    const section = (ev.data as Partial<SettingsChangedData> | null)?.section
    return section === 'usage' || section === 'profiles'
  }
  if (ev.kind === 'settings.invalid') {
    const f = (ev.data as Partial<SettingsInvalidData> | null)?.file
    return typeof f === 'string' && fileOfScript(f) !== null
  }
  return false
}
const scriptList = liveRead({
  store,
  read: () => listUsageScripts(session.client),
  apply({ data }) {
    scripts.value = data
    loadError.value = ''
    loaded.value = true
  },
  failed(e) {
    loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The usage scripts could not be read.'
    loaded.value = true
  },
  watches: concernsScripts,
})
const reload = () => scriptList.reload()

/** The open script, and whether it is one not stored yet. */
const selected = ref<string | null>(null)
const isNew = ref(false)
const editor = useOpenEditor(() => `the script ${visibleText(selected.value ?? '')}`)
const editorId = useId()
const names = computed(() => scripts.value.map((s) => s.name))
const usedBy = computed(() => scripts.value.find((s) => s.name === selected.value)?.used_by ?? [])
const usage = computed(() => limits.value.value?.usage ?? null)

async function toggle(name: string) {
  if (!(await editor.leave())) return
  selected.value = selected.value === name ? null : name
  isNew.value = false
}
async function openNew(name: string) {
  if (!(await editor.leave())) return
  selected.value = name
  isNew.value = true
}
const newButton = ref<InstanceType<typeof UiButton>>()
/** Focus after the editor of `name` went away: its row, else New script…. */
const focusAfter = (name: string) =>
  focusAfterClose(
    () => rowButton('data-script', name, 'data-script-toggle'),
    () => newButton.value?.$el as HTMLElement | undefined,
  )
async function closeNew() {
  if (!(await editor.leave())) return
  const name = selected.value
  selected.value = null
  isNew.value = false
  if (name !== null && selected.value === null) await focusAfter(name)
}
async function deleted() {
  const name = selected.value
  editor.reset()
  selected.value = null
  isNew.value = false
  await reload()
  if (name !== null && selected.value === null) await focusAfter(name)
}

const naming = ref(false)
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <UsageThresholds :form="limits" />

    <FormSection
      legend="Scripts"
      description="A script asks a provider how full an account is. It is code that runs as you, with the credentials of the profiles that use it; omo runs it once per account of those profiles."
    >
      <Callout v-if="loadError" tone="danger" live="assertive" data-scripts-error>
        <p>{{ loadError }}</p>
        <template #actions><UiButton size="sm" @click="reload()">Try again</UiButton></template>
      </Callout>
      <p v-if="!loaded" class="usage__note">Reading the scripts…</p>
      <p v-else-if="scripts.length === 0 && !loadError" class="usage__note" data-no-scripts>No usage script.</p>
      <ScriptList v-else :scripts="scripts" :selected="selected" :controls="editorId" @select="toggle" />
      <div>
        <UiButton ref="newButton" data-new-script @click="naming = true">New script…</UiButton>
      </div>

      <Callout v-if="editor.busyNote.value" tone="warn" data-editor-busy>
        <p>{{ editor.busyNote.value }}</p>
      </Callout>
      <ScriptEditor
        v-if="selected !== null"
        :key="selected"
        :name="selected"
        :is-new="isNew"
        :used-by="usedBy"
        :usage="usage"
        :form-id="editorId"
        @dirty="(d) => (editor.dirty.value = d)"
        @busy="(b) => (editor.busy.value = b)"
        @deleted="deleted"
        @close="closeNew"
      />
    </FormSection>

    <NameDialog :open="naming" :taken="names" title="New script" eyebrow="usage" thing="script" confirm="Create" @close="naming = false" @create="openNew" />
  </SettingsPage>
</template>

<style scoped>
.usage__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
