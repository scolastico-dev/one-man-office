<script setup lang="ts">
// One plugin's configuration: a JSON object the plugin reads, edited as
// indented JSON text. It has its own resource (read with the settings file's
// revision, saved with If-Match, kept current while open) and its own Save
// bar. Text that is no JSON object blocks Save with a message under the
// field; the server's refusal of `config` shows there too. Only the
// configuration is the form's: the switch and the source are saved by the
// plugin's own buttons, so a change of those elsewhere is no conflict here.
//
// The default slot is shown between the field and the Save bar (the log).
import { computed, ref, useId, watch } from 'vue'
import { getPlugins, putPlugin } from '../../../api/endpoints'
import { NO_REVISION, type ApiEvent, type PluginConfigField, type PluginInfo, type PluginSettings, type SettingsChangedData, type SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import TextArea from '../../../components/ui/TextArea.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, sameJSON, useSettingsResource } from '../useSettingsResource'
import { configText, errorsOfPlugin, fileOfPlugin, parseConfig } from './pluginForms'

interface ConfigDraft {
  config: Record<string, unknown>
}

const props = defineProps<{ name: string }>()
const emit = defineEmits<{
  /** Whether the form has unsaved edits. */
  dirty: [boolean]
  /** The server's answer to a save: the plugin's new state. */
  info: [PluginInfo]
}>()

const session = useSession()
const fieldId = useId()
const shownName = computed(() => visibleText(props.name))

/** The stored settings the last read found; a save sends them with the edited configuration. */
let stored: PluginSettings | null = null
const fields = ref<PluginConfigField[]>([])

async function readStored(): Promise<{ found: PluginSettings | undefined; errors: ReturnType<typeof errorsOfPlugin> }> {
  const { data } = await getPlugins(session.client)
  fields.value = data.plugins.find((p) => p.name === props.name)?.config_fields ?? []
  return { found: data.settings.find((s) => s.name === props.name), errors: errorsOfPlugin(data.errors ?? [], props.name) }
}

const r = useSettingsResource<ConfigDraft>({
  async load() {
    const { found, errors } = await readStored()
    if (found === undefined) throw new Error(`The plugin ${shownName.value} no longer exists.`)
    stored = found
    return { value: { config: found.config ?? {} }, revision: found.revision ?? NO_REVISION, errors }
  },
  async save(draft, revision) {
    if (stored === null) throw new Error(`The plugin ${shownName.value} no longer exists.`)
    const { data, eventId } = await putPlugin(session.client, { ...stored, revision: undefined, config: draft.config }, revision)
    emit('info', data)
    // The answer is the plugin's state; what is stored now (with its revision) is read.
    const { found } = await readStored()
    if (found === undefined) throw new Error(`The plugin ${shownName.value} no longer exists.`)
    stored = found
    return { value: { config: found.config ?? {} }, revision: found.revision ?? NO_REVISION, eventId }
  },
  watches(ev: ApiEvent) {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'plugins' && d.name === props.name
    }
    if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      return typeof d?.file === 'string' && fileOfPlugin(d.file) === props.name
    }
    return false
  },
})

useLeaveGuard(r.dirty)
watch(r.dirty, (d) => emit('dirty', d), { immediate: true })

const root = ref<HTMLElement>()
const draft = computed(() => r.draft.value)
const fe = computed(() => r.fieldErrors.value)
const unplaced = computed(() => unplacedErrors(fe.value, ['config']))
const label = computed(() => `Save changes to the configuration of ${shownName.value}`)

// The text in the field. It follows the draft when the draft changes from
// outside (a read, Discard, "Use the new value"); text that is no JSON object
// stays as typed and blocks the save.
const text = ref('')
const parseError = ref<string | null>(null)
function setParse(message: string | null) {
  parseError.value = message
  r.setParse('config', message)
}
watch(
  () => r.draft.value?.config,
  (config) => {
    if (config === undefined) return
    const now = parseConfig(text.value)
    if (now.ok && sameJSON(now.value, config)) return
    text.value = configText(config)
    setParse(null)
  },
  { immediate: true, deep: true },
)
watch(r.resets, () => {
  if (r.draft.value) text.value = configText(r.draft.value.config)
  setParse(null)
})

function onInput(value: string) {
  text.value = value
  const got = parseConfig(value)
  if (!got.ok) {
    setParse(got.message)
    return
  }
  setParse(null)
  if (r.draft.value) r.draft.value.config = got.value
}

function editField(field: PluginConfigField, value: string | boolean) {
  if (!draft.value || parseError.value) return
  const config = { ...draft.value.config }
  if ((field.type === 'select' || field.type === 'number') && value === '') delete config[field.key]
  else config[field.key] = field.type === 'number' ? Number(value) : value
  draft.value.config = config
}
function stringValue(field: PluginConfigField): string {
  const value = draft.value?.config[field.key]
  return typeof value === 'string' || typeof value === 'number' ? String(value) : ''
}
const shownError = computed(() => parseError.value ?? (fe.value['config'] ? visibleText(fe.value['config']) : undefined))

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}
</script>

<template>
  <div ref="root" class="plugin-config" :data-plugin-config="name">
    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <p v-if="r.loading.value && !draft" class="plugin-config__note">Reading the configuration…</p>
    <Callout v-else-if="!draft && r.error.value" tone="danger" live="assertive" data-settings-load-error>
      <p>{{ visibleText(r.error.value) }}</p>
      <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
    </Callout>

    <template v-if="draft">
      <UnplacedErrors :errors="unplaced" />
      <FormSection legend="Configuration" :level="4" description="A JSON object; the plugin reads it as its configuration.">
        <div v-for="f in fields" :key="f.key" class="plugin-config__primitive">
          <SelectField v-if="f.type === 'select'" :label="visibleText(f.label)" :model-value="stringValue(f)" :options="[{ value: '', label: 'Not set' }, ...(f.choices ?? []).map((c) => ({ value: c.value, label: visibleText(c.label) }))]" :disabled="!!parseError" :data-plugin-field="f.key" @update:model-value="editField(f, $event)" />
          <Toggle v-else-if="f.type === 'boolean'" :label="visibleText(f.label)" :model-value="draft.config[f.key] === true" :disabled="!!parseError" :data-plugin-field="f.key" @update:model-value="editField(f, $event)" />
          <TextField v-else :label="visibleText(f.label)" :type="f.type === 'number' ? 'number' : 'text'" :model-value="stringValue(f)" :disabled="!!parseError" :data-plugin-field="f.key" @update:model-value="editField(f, $event)" />
        </div>
        <div class="plugin-config__code">
          <TextArea
            :id="fieldId"
            :model-value="text"
            label="Configuration (JSON)"
            :rows="6"
            :error="shownError"
            spellcheck="false"
            autocomplete="off"
            autocapitalize="off"
            data-plugin-config-text
            @update:model-value="onInput"
          />
        </div>
      </FormSection>
    </template>

    <slot />

    <SaveBar
      v-if="draft"
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
  </div>
</template>

<style scoped>
.plugin-config {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
}
.plugin-config__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
/* The configuration keeps its lines; long ones scroll inside the box. */
.plugin-config__code :deep(textarea) {
  white-space: pre;
  overflow-x: auto;
  overflow-wrap: normal;
}
</style>
