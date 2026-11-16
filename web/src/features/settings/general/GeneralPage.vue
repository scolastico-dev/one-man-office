<script setup lang="ts">
// The General section of the settings: the CEO switches, how tickets open, the merge defaults
// (how finished work is merged while a project sets no mode of its own),
// facts about this server, and the theme. The theme belongs to this browser: it applies at once
// and is not part of the form.
import { computed, onMounted, ref } from 'vue'
import { getServer } from '../../../api/endpoints'
import type { MergeMode, ServerInfo } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { applyTheme, readTheme, THEME_CHOICES, type ThemeChoice } from '../../../theme'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import CeoSwitchDialog from './CeoSwitchDialog.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { MERGE_KINDS, MODE_OPTIONS } from '../projects/mergeModes'
import { useGeneralForm } from './generalForms'

const def = sectionDef('general')
const form = useGeneralForm(['ceo', 'merge_defaults', 'ui'])
const draft = form.draft
const err = (field: string) => form.fieldErrors.value[field]
const unplaced = computed(() => unplacedErrors(form.fieldErrors.value, ['ceo.enabled', 'ceo.may_approve_stories', 'ui.open_tickets_as_page', ...MERGE_KINDS.map((k) => `merge_defaults.${k.kind}`)]))
const { client } = useSession()

const askOff = ref(false)
function setCeo(on: boolean) {
  if (!draft.value) return
  if (on) draft.value.ceo.enabled = true
  else askOff.value = true
}
function confirmOff() {
  askOff.value = false
  if (draft.value) draft.value.ceo.enabled = false
}

// What this server is: asked once; the home folder comes with the settings.
const server = ref<ServerInfo | null>(null)
const serverError = ref('')
onMounted(() => {
  getServer(client).then(
    ({ data }) => (server.value = data),
    (e: unknown) => (serverError.value = e instanceof Error ? e.message : 'The server facts could not be read.'),
  )
})

/** Browser storage can be blocked; the choice then lasts until the page is closed. */
function storage(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}
function shownTheme(): ThemeChoice {
  const s = storage()
  if (s) return readTheme(s)
  const shown = document.documentElement.getAttribute('data-theme')
  return shown === 'dark' || shown === 'light' ? shown : 'system'
}
const theme = ref<ThemeChoice>(shownTheme())
function chooseTheme(choice: string) {
  theme.value = choice as ThemeChoice
  applyTheme(theme.value, document.documentElement, storage())
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" :resource="form">
    <div v-if="draft" class="general">
      <UnplacedErrors :errors="unplaced" />
      <FormSection legend="CEO" description="Applies at the next scheduling pass.">
        <Toggle :model-value="draft.ceo.enabled" label="The CEO supervises the board" data-field="ceo.enabled" @update:model-value="setCeo" />
        <p v-if="err('ceo.enabled')" class="omo-field__error" data-field-error>
          {{ err('ceo.enabled') }}
        </p>
        <Toggle v-model="draft.ceo.may_approve_stories" label="The CEO may approve stories" data-field="ceo.may_approve_stories" />
        <p v-if="err('ceo.may_approve_stories')" class="omo-field__error" data-field-error>
          {{ err('ceo.may_approve_stories') }}
        </p>
      </FormSection>
      <FormSection legend="Tickets" description="A click on a ticket opens its page instead of the panel over the board. The panel stays reachable with 'Open in panel'." data-ui-section>
        <Toggle
          v-model="draft.ui.open_tickets_as_page"
          label="Open tickets on their own page"
          data-field="ui.open_tickets_as_page"
        />
        <p v-if="err('ui.open_tickets_as_page')" class="omo-field__error" data-field-error>
          {{ err('ui.open_tickets_as_page') }}
        </p>
      </FormSection>
      <FormSection legend="Merge defaults" description="How omo merges finished work into its target branch. A project can set its own mode on the Projects page." data-merge-defaults>
        <SelectField
          v-for="k in MERGE_KINDS"
          :key="k.kind"
          :model-value="draft.merge_defaults[k.kind]"
          :label="k.label"
          :options="MODE_OPTIONS"
          :hint="k.hint"
          :error="err(`merge_defaults.${k.kind}`)"
          :data-field="`merge_defaults.${k.kind}`"
          @update:model-value="(v) => (draft!.merge_defaults[k.kind] = v as MergeMode)"
        />
      </FormSection>
      <FormSection legend="This server">
        <dl class="general__facts" data-server-facts>
          <dt>Version</dt>
          <dd>{{ server ? visibleText(server.version) : '…' }}</dd>
          <dt>Address</dt>
          <dd>{{ server ? visibleText(server.address) : '…' }}</dd>
          <dt>Home folder</dt>
          <dd>{{ form.home.value ? visibleText(form.home.value) : '…' }}</dd>
        </dl>
        <p v-if="serverError" class="omo-field__error">{{ serverError }}</p>
        <Callout tone="info">
          <p>
            Host, port and sign-in are start options: change them with
            <code>omo config</code>. They apply at the next start.
          </p>
        </Callout>
      </FormSection>
      <FormSection legend="Theme" description="Remembered in this browser.">
        <SelectField
          :model-value="theme"
          label="Colours"
          :options="[...THEME_CHOICES]"
          hint="System follows the colour scheme of your browser or operating system."
          @update:model-value="chooseTheme"
        />
      </FormSection>
    </div>
    <CeoSwitchDialog :open="askOff" @close="askOff = false" @confirm="confirmOff" />
  </SettingsPage>
</template>

<style scoped>
.general {
  display: grid;
  gap: calc(var(--omo-space) * 6);
}
.general__facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 6);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.general__facts dt {
  color: var(--omo-muted);
}
.general__facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
