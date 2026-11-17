<script setup lang="ts">
// The General section of the settings: the CEO switches, how tickets open, the branch name template
// (with a live example the server computes by the rules it names branches with), the merge defaults
// (how finished work is merged while a project sets no mode of its own),
// facts about this server, and the theme. The theme belongs to this browser: it applies at once
// and is not part of the form.
import { computed, onMounted, ref, watch } from 'vue'
import { getServer, previewBranch } from '../../../api/endpoints'
import type { MergeMode, ServerInfo } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
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
import { usePromptPreview } from '../prompts/usePromptPreview'

const def = sectionDef('general')
const form = useGeneralForm(['ceo', 'merge_defaults', 'ui', 'branches'])
const draft = form.draft
const err = (field: string) => form.fieldErrors.value[field]
const unplaced = computed(() => unplacedErrors(form.fieldErrors.value, ['ceo.enabled', 'ceo.may_approve_stories', 'ceo.tickets_need_user_approval', 'ui.open_tickets_as_page', 'ui.screen_reader_mode', 'branches.template', ...MERGE_KINDS.map((k) => `merge_defaults.${k.kind}`)]))
const { client } = useSession()

// The example name of the branch template, asked from the server while the user types, so it follows
// the server's own rules and knows this machine's host and user name.
const branchPreview = usePromptPreview({
  request: async (_name, template) => {
    const { data } = await previewBranch(client, template)
    return { text: data.branch, error: data.error }
  },
})
watch(
  () => draft.value?.branches.template,
  (tmpl) => {
    if (tmpl !== undefined) branchPreview.schedule('branches', tmpl)
  },
  { immediate: true },
)
const BRANCH_HELP =
  'Placeholders: {slug} the title in lower case with dashes (ticket when it has no letter or digit), {number} the ticket number, ' +
  '{type} story or task, {project} the project, {host} the name of this machine, {user} your user name on it. ' +
  'Other text is kept as you type it. The template must contain {slug} or {number}; a name that is taken gets -2, -3 and so on.'

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

// The version links to its commit or release; only an https address from the server is followed.
const versionLink = computed(() => (server.value && /^https:\/\//.test(server.value.version_url) ? server.value.version_url : ''))
const versionLabel = computed(() => {
  const s = server.value
  if (!s) return ''
  const what = s.version_commit ? `commit ${s.version_commit}` : 'release'
  return `omo ${visibleText(s.version)}, ${what} on GitHub`
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
        <Toggle
          v-model="draft.ceo.may_approve_stories"
          label="The CEO may approve stories"
          aria-describedby="ceo-may-approve-hint"
          data-field="ceo.may_approve_stories"
        />
        <p id="ceo-may-approve-hint" class="omo-field__hint">
          The CEO moves a story to Ready For Dev once you agreed to it in its terminal. While the CEO's tickets wait for your approval, this covers
          only the stories you created.
        </p>
        <p v-if="err('ceo.may_approve_stories')" class="omo-field__error" data-field-error>
          {{ err('ceo.may_approve_stories') }}
        </p>
        <Toggle
          v-model="draft.ceo.tickets_need_user_approval"
          label="The CEO's tickets wait for your approval"
          aria-describedby="ceo-approval-hint"
          data-field="ceo.tickets_need_user_approval"
        />
        <p id="ceo-approval-hint" class="omo-field__hint">
          Stories and tasks the CEO creates stay in Planning until you approve them, as you approve any story; the CEO cannot approve or withdraw
          them, whatever the switch above or the task release setting says. Tasks a product manager creates in a story you approved follow the task
          release setting.
        </p>
        <p v-if="err('ceo.tickets_need_user_approval')" class="omo-field__error" data-field-error>
          {{ err('ceo.tickets_need_user_approval') }}
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
      <FormSection legend="Accessibility" description="For every terminal in every browser. Screen reader software gets a text copy of the terminal output; nothing changes visibly." data-accessibility-section>
        <Toggle v-model="draft.ui.screen_reader_mode" label="Screen reader mode for terminals" data-field="ui.screen_reader_mode" />
        <p v-if="err('ui.screen_reader_mode')" class="omo-field__error" data-field-error>
          {{ err('ui.screen_reader_mode') }}
        </p>
      </FormSection>
      <FormSection legend="Branches" description="How omo names the branch it creates for a ticket. Branches that already exist keep their names." data-branches-section>
        <TextField
          v-model="draft.branches.template"
          label="Branch name template"
          :hint="BRANCH_HELP"
          :error="err('branches.template')"
          spellcheck="false"
          autocomplete="off"
          autocapitalize="off"
          data-field="branches.template"
        />
        <p
          v-if="branchPreview.error.value && !err('branches.template')"
          class="omo-field__error"
          data-branch-preview
          data-branch-preview-error
          aria-live="polite"
        >
          {{ visibleText(branchPreview.error.value) }}
        </p>
        <p v-else-if="branchPreview.failure.value && !err('branches.template')" class="omo-field__hint" data-branch-preview aria-live="polite">
          {{ visibleText(branchPreview.failure.value) }}
        </p>
        <p v-else-if="branchPreview.ready.value" class="omo-field__hint" data-branch-preview aria-live="polite">
          Example: <code>{{ visibleText(branchPreview.text.value) }}</code> for #42 "Fix the login", a task
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
          <dd>
            <template v-if="!server">…</template>
            <a
              v-else-if="versionLink"
              :href="versionLink"
              target="_blank"
              rel="noopener noreferrer"
              :aria-label="versionLabel"
              data-version-link
              >{{ visibleText(server.version) }}</a
            >
            <template v-else>{{ visibleText(server.version) }}</template>
          </dd>
          <dt>Address</dt>
          <dd>
            {{ server ? visibleText(server.address) : '…' }}
            <p v-if="server" class="general__help" data-address-help>
              <template v-if="server.address_configured">
                From the <code>public_url</code> option; omo keeps listening on {{ visibleText(server.listen) }}.
              </template>
              <template v-else>
                Set with <code>omo config set public_url &lt;address&gt;</code>; omo keeps listening on {{ visibleText(server.listen) }}.
              </template>
            </p>
          </dd>
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
.general__facts a {
  color: var(--omo-accent);
}
.general__help {
  margin: calc(var(--omo-space) * 1) 0 0;
  color: var(--omo-muted);
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
