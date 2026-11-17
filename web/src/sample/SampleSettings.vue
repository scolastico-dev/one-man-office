<script setup lang="ts">
// The settings with static data, for judging the look of the form components:
// the section list on the left, the page on the right; below 768 px the list
// is a page of its own and each section has Back. Every section shows a few
// of the new components; together they show all of them, a broken file, a
// change on disk, a refused field and each state of the Save bar.
import { computed, onBeforeUnmount, ref, useId } from 'vue'
import Callout from '../components/ui/Callout.vue'
import DurationField from '../components/ui/DurationField.vue'
import EnvTable from '../components/ui/EnvTable.vue'
import FormSection from '../components/ui/FormSection.vue'
import ListField from '../components/ui/ListField.vue'
import NumberField from '../components/ui/NumberField.vue'
import Panel from '../components/ui/Panel.vue'
import ReorderList, { type ReorderItem } from '../components/ui/ReorderList.vue'
import SaveBar from '../components/ui/SaveBar.vue'
import SelectField from '../components/ui/SelectField.vue'
import StatusBadge from '../components/ui/StatusBadge.vue'
import TextArea from '../components/ui/TextArea.vue'
import TextField from '../components/ui/TextField.vue'
import Toggle from '../components/ui/Toggle.vue'
import UiButton from '../components/ui/UiButton.vue'
import { visibleText } from '../components/ui/visibleText'
import { SLOT_RULE } from '../board/slots'
import '../layout/settingsFrame.css'
import SampleExtensions from './SampleExtensions.vue'
import SampleFolderPicker from './SampleFolderPicker.vue'
import {
  SAMPLE_SECTIONS,
  isExtensionSection,
  type SampleSection,
  sampleAgentEnv,
  sampleEnvCheck,
  sampleInstructions,
  sampleModels,
  sampleProfileArgs,
  sampleProfileEnv,
  samplePreview,
  sampleProjects,
  sampleRoleModels,
  sampleRoots,
  sampleShippedEnv,
} from './sampleSettings'

const props = defineProps<{
  /** The section shown; `null` shows the section list (below 768 px the list is its own page). */
  section: SampleSection | null
  picker: boolean
  /** On Usage, the open script's test failed. */
  testFailed?: boolean
}>()

const current = ref<SampleSection | null>(props.section)
const shown = computed(() => SAMPLE_SECTIONS.find((s) => s.id === (current.value ?? 'projects'))!)
const pickerOpen = ref(props.picker)

const narrowQuery = window.matchMedia('(max-width: 767.98px)')
const narrow = ref(narrowQuery.matches)
const onNarrow = (e: MediaQueryListEvent) => (narrow.value = e.matches)
narrowQuery.addEventListener('change', onNarrow)
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onNarrow))

const showList = computed(() => !narrow.value || current.value === null)
const showPage = computed(() => !narrow.value || current.value !== null)

function href(id: SampleSection | null): string {
  const q = new URLSearchParams(window.location.search)
  q.set('settings', id ?? 'list')
  q.delete('picker')
  return `?${q}`
}
function open(id: SampleSection | null) {
  current.value = id
  window.history.replaceState(null, '', href(id))
}

const titleId = useId()

// Projects
const roots = ref(sampleRoots.map((r) => ({ ...r, ignore: [...r.ignore] })))
const projectsOn = ref<Record<string, boolean>>(Object.fromEntries(sampleProjects.map((p) => [p.id, p.state !== 'off'])))

// Agents
const cli = ref('claude')
const command = ref('claude')
const profileArgs = ref([...sampleProfileArgs])
const profileEnv = ref({ ...sampleProfileEnv })
const account = ref('~/.claude-work')
const models = ref(sampleModels.map((m) => ({ ...m, args: [...m.args], env: { ...m.env } })))
const CLI_OPTIONS = [
  { value: 'claude', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'agy', label: 'Antigravity' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'custom', label: 'Custom' },
]

// Roles
const developerModels = ref<ReorderItem[]>([...sampleRoleModels])
const reviewerModels = ref<ReorderItem[]>([{ id: 'claude/opus', label: 'claude/opus', note: 'Strongest; for planning and hard bugs.' }])
const ceoModels = ref<ReorderItem[]>([])
const developerMode = ref('random')
const reviewerMode = ref('failover')
const addModel = ref('claude/opus')
const MODE_OPTIONS = [
  { value: 'round_robin', label: 'Round robin: each model in turn' },
  { value: 'random', label: 'Random: a model at random' },
  { value: 'failover', label: 'Failover: the first that can start' },
  { value: 'smart', label: 'Smart: the most capacity left' },
]
const MODEL_OPTIONS = [
  { value: 'claude/opus', label: 'claude/opus' },
  { value: 'claude/sonnet', label: 'claude/sonnet' },
  { value: 'claude/fable', label: 'claude/fable (agents cannot pick it)' },
]
function moveIn(list: ReorderItem[], from: number, to: number) {
  const [item] = list.splice(from, 1)
  list.splice(to, 0, item!)
}
const weight = (list: ReorderItem[], id: string) => list.filter((m) => m.id === id).length

// Prompts
const instructions = ref(sampleInstructions)
const stalled = ref('{{.Ticket.Number}} has shown no output for {{.Silent}}.\nIf you are stuck, say so on the ticket: {{nme .Ticket}}')

// Environment
const agentEnv = ref({ ...sampleAgentEnv, NPM_TOKEN: '`pass show npm/token`' })
const CHECK_WORDS = { set: 'set', empty: 'empty', failed: 'failed' } as const

// Limits
const taskLimit = ref<number | null>(4)
const storyLimit = ref<number | null>(2)
const silentAfter = ref('30s')
const unanswered = ref('1h')
const timeouts = ref<number | null>(3)
const handoff = ref('2m')
const rescan = ref('1h')

// Review
const REVIEW_OPTIONS = [
  { value: 'skipped', label: 'Skipped' },
  { value: 'agent', label: 'Agent' },
  { value: 'human', label: 'Human' },
  { value: 'agent_then_human', label: 'Agent, then human' },
]
const APPROVED_OPTIONS = [
  { value: 'merge', label: 'Merge locally' },
  { value: 'pull_request', label: 'Export as a pull request' },
]
const RELEASE_OPTIONS = [
  { value: 'agents', label: 'The product manager or the CEO' },
  { value: 'user', label: 'You' },
]
const storyReview = ref('human')
const storyApproved = ref('pull_request')
const taskReview = ref('agent')
const taskApproved = ref('merge')
const release = ref('agents')

// General
const ceoOn = ref(true)
const ceoApproves = ref(false)
const theme = ref(document.documentElement.getAttribute('data-theme') ?? 'system')
const THEME_OPTIONS = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark' },
  { value: 'light', label: 'Light' },
]

// What the Save bar of each section shows.
// Usage, Skill sets and Plugins have several forms, each with its own bar.
const SAVE: Record<Exclude<SampleSection, 'usage' | 'skills' | 'plugins'>, { dirty: boolean; status?: 'saved' | 'conflict'; error?: string }> = {
  projects: { dirty: true },
  agents: { dirty: true, status: 'conflict' },
  roles: { dirty: false, status: 'saved' },
  prompts: { dirty: true, error: 'template: stalled:2: function "nme" not defined' },
  environment: { dirty: true },
  limits: { dirty: true, error: 'health.silent_after must be at least 1m' },
  review: { dirty: false },
  general: { dirty: false },
}
const save = computed(() => {
  const id = shown.value.id
  return isExtensionSection(id) ? null : SAVE[id]
})
</script>

<template>
  <div class="settings settings-frame" :class="{ 'settings--narrow': narrow }" data-settings>
    <div v-if="showList" class="settings__side">
      <h1 v-if="!showPage" class="sr-only">Settings</h1>
      <Panel as="nav" label="Settings" :padded="false">
        <ul class="settings__sections">
          <li v-for="s in SAMPLE_SECTIONS" :key="s.id">
            <a
              :href="href(s.id)"
              class="settings__link"
              :aria-current="!narrow && s.id === shown.id ? 'page' : undefined"
              @click.prevent="open(s.id)"
            >
              <span class="settings__link-label">{{ s.label }}</span>
              <span class="settings__link-desc">{{ s.description }}</span>
            </a>
          </li>
        </ul>
      </Panel>
    </div>

    <section v-if="showPage" class="settings__page" :aria-labelledby="titleId">
      <div class="settings__column">
        <header class="settings__head">
          <a v-if="narrow" :href="href(null)" class="settings__back" @click.prevent="open(null)">
            <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M8 2L4 6l4 4" />
            </svg>
            Settings
          </a>
          <div class="settings__title-row">
            <div class="min-w-0">
              <h1 :id="titleId" class="settings__title">{{ shown.label }}</h1>
              <p class="settings__desc">{{ shown.description }}.</p>
            </div>
            <div v-if="shown.id === 'projects'" class="settings__actions">
              <p class="settings__scan">Last scan 3 min ago: 2 added</p>
              <UiButton size="sm">Rescan</UiButton>
            </div>
          </div>
        </header>

        <div class="settings__body">
          <!-- Projects -->
          <template v-if="shown.id === 'projects'">
            <FormSection legend="Workspace folders" description="Repositories found here become projects at once. Folders inside a repository are not searched; links are not followed.">
              <div v-for="(root, i) in roots" :key="i" class="sample-root">
                <div class="sample-path">
                  <TextField v-model="root.path" :label="`Folder ${i + 1}`" spellcheck="false" autocomplete="off" />
                  <UiButton class="sample-path__browse" @click="pickerOpen = true">Browse…</UiButton>
                </div>
                <NumberField v-model="root.maxDepth" label="Search depth" :min="1" :max="16" hint="How many folders deep to look, 1 to 16." />
                <ListField
                  v-model="root.ignore"
                  label="Ignore"
                  item-label="Pattern"
                  add-label="Add pattern"
                  empty-text="Nothing is ignored."
                  hint="A folder name such as node_modules, or a path below the folder such as archive/*."
                />
                <div>
                  <UiButton size="sm" variant="danger">Remove folder {{ i + 1 }}</UiButton>
                </div>
              </div>
              <div>
                <UiButton @click="pickerOpen = true">Add workspace folder…</UiButton>
              </div>
            </FormSection>

            <FormSection legend="Projects" description="Switched off, a project keeps its tickets but takes no new ones.">
              <ul class="sample-projects">
                <li v-for="p in sampleProjects" :key="p.id" class="sample-project">
                  <div class="min-w-0">
                    <p class="sample-project__name">{{ visibleText(p.name) }}</p>
                    <p class="sample-project__path">{{ visibleText(p.path) }}</p>
                    <p class="sample-project__root">{{ p.root ? `found in ${p.root}` : 'added by hand' }}</p>
                    <p v-if="p.state === 'missing'" class="sample-project__missing">The folder is gone. New tickets cannot use it.</p>
                  </div>
                  <div class="sample-project__side">
                    <StatusBadge v-if="p.state === 'missing'" tone="danger" text="missing" />
                    <Toggle v-else v-model="projectsOn[p.id]" :label="`Use ${p.name}`" hide-label />
                  </div>
                </li>
              </ul>
              <div>
                <UiButton size="sm" variant="danger">Delete all missing projects…</UiButton>
              </div>
            </FormSection>
          </template>

          <!-- Agents -->
          <template v-else-if="shown.id === 'agents'">
            <Callout tone="danger" title="profiles/codex.yaml">
              <p>Cannot be read: line 4: mapping values are not allowed in this context. omo uses the last version it could read.</p>
            </Callout>
            <Callout tone="warn" live="polite">
              <p>Changed on disk at 14:02, not through omo.</p>
            </Callout>

            <FormSection legend="Profile claude" description="Applies to agents started after the save; running agents keep what they started with.">
              <TextField model-value="claude" label="Name" disabled hint="Fixed after creation. Duplicate… makes a copy under a new name." />
              <SelectField v-model="cli" label="Agent program" :options="CLI_OPTIONS" />
              <TextField v-model="command" label="Command" spellcheck="false" autocomplete="off" />
              <ListField v-model="profileArgs" label="Base arguments" item-label="Argument" add-label="Add argument" empty-text="No arguments." />
              <EnvTable
                v-model="profileEnv"
                label="Environment"
                hint="Overrides the agent environment. Values are used exactly as typed: $VAR and `command` are neither expanded nor run here, only in the agent environment on the Environment page."
              />
              <div class="sample-path">
                <TextField v-model="account" label="Account folder" hint="Becomes CLAUDE_CONFIG_DIR for this profile's agents." spellcheck="false" />
                <UiButton class="sample-path__browse" @click="pickerOpen = true">Browse…</UiButton>
              </div>
            </FormSection>

            <FormSection v-for="m in models" :key="m.name" :legend="`Model ${m.name}`">
              <ListField v-model="m.args" label="Extra arguments" item-label="Argument" add-label="Add argument" empty-text="No extra arguments." />
              <EnvTable v-model="m.env" label="Extra environment" empty-text="No extra variables." />
              <Toggle v-model="m.selectable" label="Agents may choose it for tickets they create" />
              <TextField v-model="m.description" label="Description" hint="One line, shown where models are chosen." />
              <div>
                <UiButton size="sm" variant="danger">Remove model {{ m.name }}</UiButton>
              </div>
            </FormSection>
          </template>

          <!-- Roles -->
          <template v-else-if="shown.id === 'roles'">
            <FormSection legend="Developer" description="Works on tasks: writes the code and its tests.">
              <ReorderList
                :items="developerModels"
                label="Models of the developer"
                @move="(from, to) => moveIn(developerModels, from, to)"
                @remove="(i) => developerModels.splice(i, 1)"
              >
                <template #item="{ item }">
                  <span class="sample-model">
                    {{ item.label }}<span v-if="weight(developerModels, item.id) > 1" class="sample-model__weight"> ×{{ weight(developerModels, item.id) }}</span>
                    <StatusBadge v-if="item.id === 'codex/default'" tone="danger" text="does not exist" />
                  </span>
                  <span class="sample-model__note">{{ item.id === 'codex/default' ? 'No profile codex. Remove it or add the profile.' : item.note }}</span>
                </template>
              </ReorderList>
              <div class="sample-add">
                <SelectField v-model="addModel" label="Add model" :options="MODEL_OPTIONS" />
                <UiButton class="sample-path__browse">Add</UiButton>
              </div>
              <SelectField v-model="developerMode" label="Assignment" :options="MODE_OPTIONS" />
              <Callout tone="info"><p>With round robin and random, a model listed twice is picked twice as often; with failover and smart it counts once.</p></Callout>
            </FormSection>
            <FormSection legend="Reviewer">
              <ReorderList
                :items="reviewerModels"
                label="Models of the reviewer"
                @move="(from, to) => moveIn(reviewerModels, from, to)"
                @remove="(i) => reviewerModels.splice(i, 1)"
              />
              <SelectField v-model="reviewerMode" label="Assignment" :options="MODE_OPTIONS" />
            </FormSection>
            <FormSection legend="CEO">
              <ReorderList :items="ceoModels" label="Models of the CEO" empty-text="No models yet. The CEO cannot start." />
            </FormSection>
          </template>

          <!-- Prompts -->
          <template v-else-if="shown.id === 'prompts'">
            <FormSection legend="Developer" description="Your instructions are inserted into the developer's prompt. Empty, the prompt has no such part.">
              <TextArea v-model="instructions" label="Your instructions" :rows="6" spellcheck="false" />
              <div class="sample-buttons">
                <UiButton size="sm">Preview</UiButton>
                <UiButton size="sm" variant="ghost">Show omo's prompt for this role</UiButton>
              </div>
              <div class="sample-preview">
                <p class="sample-preview__label">Preview with sample data</p>
                <pre class="sample-preview__text">{{ samplePreview }}</pre>
              </div>
            </FormSection>
            <FormSection legend="Stalled" description="Typed into an agent that has shown no output for the silent time.">
              <TextArea v-model="stalled" label="Prompt" :rows="4" spellcheck="false" error='template: stalled:2: function "nme" not defined' />
              <div class="sample-buttons">
                <UiButton size="sm">Preview</UiButton>
                <UiButton size="sm" variant="ghost">Reset to omo's default…</UiButton>
              </div>
            </FormSection>
          </template>

          <!-- Environment -->
          <template v-else-if="shown.id === 'environment'">
            <FormSection legend="Variables">
              <template #description>
                <p>Every agent starts with these. <code>$VAR</code> and <code>${VAR:-fallback}</code> use omo's own environment; <code>`command`</code> runs at agent start and uses its output.</p>
                <p>Profile and model environments override these. omo's own git client uses them for merge commits.</p>
              </template>
              <EnvTable v-model="agentEnv" label="Agent environment" :shipped="sampleShippedEnv" />
              <div class="sample-buttons">
                <UiButton size="sm">Check</UiButton>
                <UiButton size="sm" variant="ghost">Restore shipped defaults…</UiButton>
              </div>
              <Callout tone="danger" title="1 of 5 variables failed" live="polite">
                <ul class="sample-check">
                  <li v-for="c in sampleEnvCheck" :key="c.name" :class="`sample-check--${c.state}`">
                    <code>{{ c.name }}</code> <span class="sample-check__state">{{ CHECK_WORDS[c.state] }}</span><span v-if="c.reason">: {{ c.reason }}</span>
                  </li>
                </ul>
              </Callout>
            </FormSection>
          </template>

          <!-- Limits -->
          <template v-else-if="shown.id === 'limits'">
            <FormSection legend="Work in progress" :description="SLOT_RULE">
              <NumberField v-model="taskLimit" label="Tasks in work" :min="1" :max="64" hint="Tasks inside a story count too. 1 to 64." />
              <NumberField v-model="storyLimit" label="Stories in work" :min="1" :max="64" hint="1 to 64." />
            </FormSection>
            <FormSection legend="Health">
              <DurationField v-model="silentAfter" label="Silent after" hint="An agent without output this long is nudged." error="health.silent_after must be at least 1m" />
              <DurationField v-model="unanswered" label="Question unanswered after" hint="A question to you this old rings the bell again." />
              <NumberField v-model="timeouts" label="Timeouts before investigating" :min="1" :max="100" hint="Waits that ran out before the product manager looks into it." />
            </FormSection>
            <FormSection legend="Stop and scan">
              <DurationField v-model="handoff" label="Handoff time limit at stop" hint="How long omo stop waits for handoff comments." />
              <DurationField v-model="rescan" label="Rescan interval" hint="How often the workspace folders are searched." />
            </FormSection>
          </template>

          <!-- Review -->
          <template v-else-if="shown.id === 'review'">
            <FormSection legend="Stories" description="Apply to tickets that set no value of their own.">
              <SelectField v-model="storyReview" label="Review" :options="REVIEW_OPTIONS" hint="No agent runs the full test suite for stories." />
              <SelectField v-model="storyApproved" label="When approved" :options="APPROVED_OPTIONS" />
            </FormSection>
            <FormSection legend="Tasks" description="Apply to tickets that set no value of their own.">
              <SelectField v-model="taskReview" label="Review" :options="REVIEW_OPTIONS" />
              <SelectField v-model="taskApproved" label="When approved" :options="APPROVED_OPTIONS" hint="Tasks inside a story always merge into the story's branch." />
              <SelectField v-model="release" label="Tasks are released by" :options="RELEASE_OPTIONS" />
            </FormSection>
          </template>

          <SampleExtensions v-else-if="isExtensionSection(shown.id)" :section="shown.id" :test="testFailed ? 'error' : 'ok'" />

          <!-- General -->
          <template v-else>
            <FormSection legend="CEO">
              <Toggle v-model="ceoOn" label="The CEO supervises the board" />
              <Toggle v-model="ceoApproves" label="The CEO may approve stories" />
            </FormSection>
            <FormSection legend="This server">
              <dl class="sample-facts">
                <dt>Version</dt>
                <dd>0.9.0</dd>
                <dt>Address</dt>
                <dd>http://127.0.0.1:8090</dd>
                <dt>Home folder</dt>
                <dd>/home/you/.local/omo</dd>
              </dl>
              <Callout tone="info">
                <p>Host, port and sign-in are start options: change them with <code>omo config</code>. They apply at the next start.</p>
              </Callout>
            </FormSection>
            <FormSection legend="Theme" description="Remembered in this browser.">
              <SelectField v-model="theme" label="Colours" :options="THEME_OPTIONS" />
            </FormSection>
          </template>
        </div>

        <SaveBar v-if="save" :dirty="save.dirty" :busy="false" :status="save.status ?? null" :error="save.error">
          <template v-if="save.status === 'conflict'">
            <UiButton size="sm">Use the new value</UiButton>
            <UiButton size="sm">Keep mine</UiButton>
          </template>
        </SaveBar>
      </div>
    </section>

    <SampleFolderPicker :open="pickerOpen" title="Choose a workspace folder" @close="pickerOpen = false" @choose="pickerOpen = false" />
  </div>
</template>

<style scoped>
.settings {
  display: grid;
  grid-template-columns: var(--settings-list) minmax(0, 1fr);
  align-items: start;
  gap: var(--settings-gap);
  height: 100%;
  min-height: 0;
  /* The frame is the scroll box (as in the real settings): the wheel works anywhere in it. */
  overflow-y: auto;
  container-type: size;
}
.settings--narrow {
  grid-template-columns: minmax(0, 1fr);
}
/* Sticks while the page scrolls; the padding keeps the panel's label, which sits on the top border, in view. */
.settings__side {
  position: sticky;
  top: 0;
  max-height: 100cqh;
  padding-top: calc(var(--omo-space) * 2.5);
  overflow-y: auto;
}
.settings--narrow .settings__side {
  position: static;
  padding-right: var(--settings-edge);
}
.settings__sections {
  margin: 0;
  padding: calc(var(--omo-space) * 4) 0 calc(var(--omo-space) * 2);
  list-style: none;
}
.settings__link {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border-left: var(--omo-edge) solid transparent;
  color: var(--omo-fg);
  text-decoration: none;
  transition:
    background-color var(--omo-duration) ease,
    border-color var(--omo-duration) ease;
}
.settings__link:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.settings__link[aria-current='page'] {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.settings__link-label {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.settings__link[aria-current='page'] .settings__link-label {
  font-weight: 700;
}
.settings__link-desc {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@media (hover: hover) {
  .settings__link:hover:not([aria-current='page']) {
    border-left-color: var(--omo-line-strong);
    background: var(--omo-accent-soft);
  }
}
.settings__page {
  align-self: stretch;
  padding-top: calc(var(--omo-space) * 2.5);
  min-width: 0;
  padding-right: var(--settings-edge);
}
.settings__column {
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 5);
  max-width: var(--settings-page-max);
  min-height: 100%;
}
.settings__head {
  display: grid;
  gap: calc(var(--omo-space) * 2);
}
.settings__back {
  display: inline-flex;
  align-items: center;
  justify-self: start;
  gap: calc(var(--omo-space) * 1.5);
  min-height: var(--omo-control-h-sm);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-decoration: none;
}
.settings__title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3) calc(var(--omo-space) * 6);
}
.settings__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  line-height: var(--omo-text-xl-lh);
}
.settings__desc {
  margin: var(--omo-space) 0 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.settings__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.settings__scan {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.settings__body {
  display: grid;
  flex: 1 0 auto;
  align-content: start;
  gap: calc(var(--omo-space) * 6);
  min-width: 0;
  padding-top: calc(var(--omo-space) * 2);
}
@media (hover: hover) {
  .settings__back:hover {
    color: var(--omo-accent);
  }
}

.sample-root {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
}
.sample-path,
.sample-add {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the input below a one-line label. */
.sample-path__browse {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
}
.sample-projects {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.sample-project {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.sample-project p {
  margin: 0;
  overflow-wrap: anywhere;
}
.sample-project__name {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.sample-project__path,
.sample-project__root {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.sample-project__missing {
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.sample-project__side {
  flex-shrink: 0;
}
.sample-model {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.sample-model__weight {
  color: var(--omo-accent);
}
.sample-model__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.sample-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.sample-preview {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
}
.sample-preview__label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.sample-preview__text {
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow-x: auto;
  font-family: inherit;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.sample-check {
  display: grid;
  gap: var(--omo-space);
  margin: 0;
  padding: 0;
  list-style: none;
}
.sample-check__state {
  color: var(--omo-ok);
}
.sample-check--empty .sample-check__state {
  color: var(--omo-warn);
}
.sample-check--failed .sample-check__state {
  color: var(--omo-danger);
  font-weight: 700;
}
.sample-facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 6);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.sample-facts dt {
  color: var(--omo-muted);
}
.sample-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
