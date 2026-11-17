<script setup lang="ts">
// The usage, skill set and plugin settings pages with static data, as the
// real pages should look: Usage with its limits, the script list, an open
// script with its test; Skill sets with one set checked out and one failed;
// Plugins with pushover from omo's list, a local plugin in error, its log and its
// actions. Each form has its own Save bar, as on pages with several forms.
import { computed, ref, useId } from 'vue'
import Callout from '../components/ui/Callout.vue'
import DurationField from '../components/ui/DurationField.vue'
import FormSection from '../components/ui/FormSection.vue'
import IconButton from '../components/ui/IconButton.vue'
import NumberField from '../components/ui/NumberField.vue'
import RelativeTime from '../components/ui/RelativeTime.vue'
import SaveBar from '../components/ui/SaveBar.vue'
import SelectField from '../components/ui/SelectField.vue'
import StatusBadge from '../components/ui/StatusBadge.vue'
import TextArea from '../components/ui/TextArea.vue'
import TextField from '../components/ui/TextField.vue'
import Toggle from '../components/ui/Toggle.vue'
import UiButton from '../components/ui/UiButton.vue'
import UsageMeter from '../components/ui/UsageMeter.vue'
import { visibleLines, visibleText } from '../components/ui/visibleText'
import {
  SAMPLE_SKILL_ROLES,
  type SamplePluginHook,
  type SamplePluginInfo,
  type SamplePluginState,
  type SampleScriptInfo,
  type SampleSkillSetState,
  sampleActionResult,
  sampleHelpers,
  samplePluginLog,
  samplePlugins,
  sampleProfiles,
  sampleScripts,
  sampleScriptText,
  sampleSkillSets,
  sampleSkillStates,
  sampleTestFailed,
  sampleTestOk,
  sampleUsageSettings,
  sampleMeter,
} from './sampleExtensions'

const props = defineProps<{
  section: 'usage' | 'skills' | 'plugins'
  /** Which test result the open script shows. */
  test: 'ok' | 'error'
}>()

// Usage

const soft = ref<number | null>(sampleUsageSettings.soft_percent)
const hard = ref<number | null>(sampleUsageSettings.hard_percent)
const refresh = ref(sampleUsageSettings.refresh_interval)
const overrides = ref(
  Object.entries(sampleUsageSettings.windows).map(([name, t]) => ({
    name,
    soft: t.soft_percent === undefined ? '' : String(t.soft_percent),
    hard: t.hard_percent === undefined ? '' : String(t.hard_percent),
  })),
)
const overridesLabelId = useId()

const SCRIPT_KIND = (s: SampleScriptInfo) => (!s.has_default ? 'Yours' : s.customized ? 'Customized' : "omo's default")
const openScript = ref('claude')
const scriptEditorId = useId()
const scriptText = ref(sampleScriptText)
const testProfile = ref(props.test === 'error' ? sampleTestFailed.profile : sampleTestOk.profile)
const testResult = computed(() => (testProfile.value === sampleTestFailed.profile ? sampleTestFailed : sampleTestOk))
const PROFILE_OPTIONS = sampleProfiles.map((p) => ({ value: p, label: sampleScripts[0]!.used_by.includes(p) ? `${p} (uses this script)` : p }))

// Skill sets

const SKILL_STATE: Record<SampleSkillSetState['state'], { tone: 'ok' | 'danger' | 'muted' | 'accent'; text: string }> = {
  ok: { tone: 'ok', text: 'checked out' },
  updating: { tone: 'accent', text: 'updating' },
  failed: { tone: 'danger', text: 'failed' },
  missing: { tone: 'muted', text: 'not checked out' },
}
const stateOf = (name: string) => sampleSkillStates.find((s) => s.name === name)!
const openSet = ref('superpowers')
const setEditorId = useId()
const set = ref({ ...sampleSkillSets[0]!, instructions: { ...sampleSkillSets[0]!.instructions } })
const UPDATE_OPTIONS = [
  { value: 'at_start', label: 'Update at every start' },
  { value: 'pinned', label: 'Keep this version (pinned)' },
]
const updateWords = (u: 'at_start' | 'pinned', ref: string) => (u === 'pinned' ? `pinned to ${ref || 'the default branch'}` : `updates at every start${ref ? ` from ${ref}` : ''}`)
const short = (commit: string) => commit.slice(0, 7)

// Plugins

const PLUGIN_STATE: Record<SamplePluginState, { tone: 'ok' | 'danger' | 'muted' | 'accent' | 'warn'; text: string }> = {
  ready: { tone: 'ok', text: 'ready' },
  running: { tone: 'accent', text: 'running' },
  error: { tone: 'danger', text: 'error' },
  disabled: { tone: 'muted', text: 'off' },
  not_installed: { tone: 'warn', text: 'not installed' },
  invalid: { tone: 'danger', text: 'invalid' },
}
const EVENT_WORDS: Record<string, string> = {
  ticket_created: 'When a ticket is created',
  ticket_moved: 'When a ticket is moved',
  comment_written: 'When a comment is written',
  question_asked: 'When an agent asks a question',
  ticket_needs_attention: 'When a ticket needs attention',
  agent_started: 'When an agent starts',
  agent_exited: 'When an agent ends',
}
/** What a run was for, after "on". */
const EVENT_NOUNS: Record<string, string> = {
  ticket_created: 'a new ticket',
  ticket_moved: 'a moved ticket',
  comment_written: 'a comment',
  question_asked: 'a question',
  ticket_needs_attention: 'a ticket that needs attention',
  agent_started: 'an agent start',
  agent_exited: 'an agent end',
  timer: 'its timer',
  manual: 'a button',
}
function hookWords(h: SamplePluginHook): string {
  if (h.event === 'timer') return `Every ${h.interval}`
  if (h.event === 'manual') return `${h.scope === 'ticket' ? 'Button on a ticket' : 'Button here'}: ${visibleText(h.description)}`
  return EVENT_WORDS[h.event] ?? h.event
}
const openPlugin = ref('ticket-log')
const pluginDetailId = useId()
const plugin = computed<SamplePluginInfo>(() => samplePlugins.find((p) => p.name === openPlugin.value)!)
const pluginOn = ref<Record<string, boolean>>(Object.fromEntries(samplePlugins.map((p) => [p.name, p.enabled])))
const configText = ref(JSON.stringify(samplePlugins[1]!.config, null, 2))
const globalActions = computed(() => plugin.value.hooks.filter((h) => h.event === 'manual' && h.scope === 'global'))
const logText = samplePluginLog.map((l) => `${new Date(l.at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}  ${l.message}`).join('\n')
</script>

<template>
  <!-- Usage -->
  <template v-if="section === 'usage'">
    <FormSection legend="Limits" description="A model's usage is the fullest window that counts for it.">
      <div class="xs-pair">
        <NumberField v-model="soft" label="Soft limit, %" :min="1" :max="100" hint="From here no new agent starts on the model." />
        <NumberField v-model="hard" label="Hard limit, %" :min="1" :max="100" hint="From here running agents hand over and stop." />
      </div>
      <DurationField v-model="refresh" label="Refresh every" hint="How often omo runs the script of each account." />
      <div class="omo-field xs-over">
        <p :id="overridesLabelId" class="omo-field__label">Limits per window</p>
        <div role="group" :aria-labelledby="overridesLabelId" class="xs-over__group">
          <div class="xs-over__head" aria-hidden="true">
            <span>Window</span>
            <span>Soft limit</span>
            <span>Hard limit</span>
          </div>
          <ul class="xs-over__rows">
            <li v-for="(o, i) in overrides" :key="i" class="xs-over__row">
              <div class="xs-over__cell xs-over__cell--name">
                <span class="xs-over__cell-label" aria-hidden="true">Window</span>
                <input v-model="o.name" class="omo-input" :aria-label="`Window ${i + 1}`" spellcheck="false" autocomplete="off" />
              </div>
              <div class="xs-over__cell">
                <span class="xs-over__cell-label" aria-hidden="true">Soft limit</span>
                <input v-model="o.soft" class="omo-input" inputmode="numeric" :aria-label="`Soft limit of ${o.name}`" placeholder="90" />
              </div>
              <div class="xs-over__cell">
                <span class="xs-over__cell-label" aria-hidden="true">Hard limit</span>
                <input v-model="o.hard" class="omo-input" inputmode="numeric" :aria-label="`Hard limit of ${o.name}`" placeholder="98" />
              </div>
              <IconButton :label="`Remove the limits of ${o.name}`" class="xs-over__remove">
                <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                  <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
                </svg>
              </IconButton>
            </li>
          </ul>
          <div>
            <UiButton size="sm">
              <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                <path d="M6 1.5v9M1.5 6h9" stroke="currentColor" stroke-width="1.5" />
              </svg>
              Add window
            </UiButton>
          </div>
        </div>
        <p class="omo-field__hint">In percent; an empty field uses the limit above. Window names come from the scripts.</p>
      </div>
      <SaveBar :dirty="false" :busy="false" status="saved" label="Save changes to the limits" />
    </FormSection>

    <FormSection legend="Scripts" description="A script asks a provider how full an account is. omo runs it once per account of the profiles that name it.">
      <ul class="xs-list">
        <li v-for="s in sampleScripts" :key="s.name" class="xs-row" :class="{ 'xs-row--open': openScript === s.name }">
          <div class="xs-row__main">
            <p class="xs-row__name">
              <span>{{ visibleText(s.name) }}</span>
              <StatusBadge :tone="s.has_default && !s.customized ? 'muted' : 'accent'" :text="SCRIPT_KIND(s)" />
            </p>
            <p class="xs-row__meta">{{ s.used_by.length ? `Used by ${s.used_by.map(visibleText).join(', ')}` : 'No profile uses it.' }}</p>
          </div>
          <UiButton
            size="sm"
            :aria-expanded="openScript === s.name ? 'true' : 'false'"
            :aria-controls="openScript === s.name ? scriptEditorId : undefined"
            @click="openScript = openScript === s.name ? '' : s.name"
          >
            {{ openScript === s.name ? 'Close' : 'Edit' }}<span class="sr-only"> {{ s.name }}</span>
          </UiButton>
        </li>
      </ul>
      <div>
        <UiButton>New script…</UiButton>
      </div>

      <div v-if="openScript === 'claude'" :id="scriptEditorId" class="xs-editor">
        <h3 class="xs-editor__title">Script claude</h3>
        <Callout tone="warn" live="polite"><p>Changed on disk at 14:02, not through omo.</p></Callout>
        <div class="xs-code"><TextArea v-model="scriptText" label="Lua" :rows="16" spellcheck="false" /></div>
        <div class="xs-buttons">
          <UiButton size="sm">Show default</UiButton>
          <UiButton size="sm" variant="ghost">Reset to omo's default…</UiButton>
        </div>
        <details class="xs-ref">
          <summary>Helpers a script can call</summary>
          <dl class="xs-ref__list">
            <template v-for="[call, what] in sampleHelpers" :key="call">
              <dt><code>{{ call }}</code></dt>
              <dd>{{ what }}</dd>
            </template>
          </dl>
        </details>

        <FormSection legend="Test" :level="4" description="Runs the text above, saved or not, as the account of a profile. Nothing is stored.">
          <div class="xs-test">
            <SelectField v-model="testProfile" label="Run as" :options="PROFILE_OPTIONS" />
            <UiButton class="xs-test__run" variant="primary">Test</UiButton>
          </div>
          <div class="xs-result" role="status" aria-label="Test result">
            <p class="xs-result__head">
              Ran as {{ visibleText(testResult.profile) }} · took {{ testResult.millis }} ms
            </p>
            <Callout v-if="testResult.error" tone="danger" title="The script failed">
              <p>{{ visibleText(testResult.error) }}</p>
            </Callout>
            <ul v-if="testResult.windows.length" class="xs-meters" aria-label="Windows">
              <li v-for="m in testResult.windows.map((w) => sampleMeter(w, { error: testResult.error, fetched_at: new Date().toISOString() }))" :key="m.name">
                <UsageMeter :label="m.shown" :percent="m.percent" :state="m.state" :soft="m.soft" :hard="m.hard" :resets-at="m.resetsAt" />
              </li>
            </ul>
            <div v-if="testResult.log.length" class="xs-log">
              <p class="xs-log__label">Log</p>
              <pre class="xs-log__text" tabindex="0" aria-label="Log of the test">{{ visibleLines(testResult.log.join('\n')) }}</pre>
            </div>
          </div>
        </FormSection>

        <SaveBar :dirty="true" :busy="false" label="Save changes to the script claude" />
      </div>
    </FormSection>
  </template>

  <!-- Skill sets -->
  <template v-else-if="section === 'skills'">
    <FormSection legend="Skill sets" description="Git repositories of skills. omo keeps one read-only copy of each and tells the agents of each role where it is.">
      <ul class="xs-list">
        <li v-for="s in sampleSkillSets" :key="s.name" class="xs-row" :class="{ 'xs-row--open': openSet === s.name }">
          <div class="xs-row__main">
            <p class="xs-row__name">
              <span>{{ visibleText(s.name) }}</span>
              <StatusBadge :tone="SKILL_STATE[stateOf(s.name).state].tone" :text="SKILL_STATE[stateOf(s.name).state].text" />
              <StatusBadge v-if="!s.enabled" tone="muted" text="off" />
            </p>
            <p class="xs-row__meta">
              {{ updateWords(s.update, s.ref) }}
              <template v-if="stateOf(s.name).state === 'ok'">
                · at <code>{{ short(stateOf(s.name).commit) }}</code>, updated <RelativeTime :at="stateOf(s.name).updated_at!" />
              </template>
            </p>
            <p v-if="stateOf(s.name).state === 'failed'" class="xs-row__error">{{ visibleText(stateOf(s.name).message) }}</p>
          </div>
          <UiButton
            size="sm"
            :aria-expanded="openSet === s.name ? 'true' : 'false'"
            :aria-controls="openSet === s.name ? setEditorId : undefined"
            @click="openSet = openSet === s.name ? '' : s.name"
          >
            {{ openSet === s.name ? 'Close' : 'Edit' }}<span class="sr-only"> {{ s.name }}</span>
          </UiButton>
        </li>
      </ul>
      <div class="xs-buttons">
        <UiButton>Add skill set…</UiButton>
      </div>

      <div v-if="openSet === 'superpowers'" :id="setEditorId" class="xs-editor">
        <h3 class="xs-editor__title">Skill set superpowers</h3>
        <TextField v-model="set.repository" label="Repository" hint="https, ssh or a local folder" spellcheck="false" autocomplete="off" />
        <TextField v-model="set.ref" label="Branch or tag" hint="Empty: the default branch." spellcheck="false" autocomplete="off" />
        <SelectField v-model="set.update" label="Updates" :options="UPDATE_OPTIONS" />
        <Toggle v-model="set.enabled" label="Agents are told about it" />
        <div class="xs-state">
          <p class="xs-state__text">
            Checked out at <code>{{ short(stateOf('superpowers').commit) }}</code>, updated <RelativeTime :at="stateOf('superpowers').updated_at!" />,
            in <span class="xs-path">{{ visibleText(stateOf('superpowers').path) }}</span>
          </p>
          <UiButton size="sm">Update now</UiButton>
        </div>
        <FormSection legend="What agents are told" :level="4" description="omo puts the folder's path above this text. A role with no text is not told about this set.">
          <TextArea
            v-for="r in SAMPLE_SKILL_ROLES"
            :key="r.id"
            v-model="set.instructions[r.id]"
            :label="r.label"
            :rows="2"
          />
        </FormSection>
        <div>
          <UiButton size="sm" variant="danger">Delete superpowers…</UiButton>
        </div>
        <SaveBar :dirty="true" :busy="false" label="Save changes to the skill set superpowers" />
      </div>
    </FormSection>
  </template>

  <!-- Plugins -->
  <template v-else>
    <FormSection legend="Installed" description="Plugins react to what happens on the board. They run as you, with your rights.">
      <ul class="xs-list">
        <li v-for="p in samplePlugins" :key="p.name" class="xs-row" :class="{ 'xs-row--open': openPlugin === p.name }">
          <div class="xs-row__main">
            <p class="xs-row__name">
              <span>{{ visibleText(p.name) }}</span>
              <span class="xs-row__version">{{ visibleText(p.version) }}</span>
              <StatusBadge :tone="PLUGIN_STATE[p.state].tone" :text="PLUGIN_STATE[p.state].text" />
            </p>
            <p class="xs-row__desc">{{ visibleText(p.description) }}</p>
            <p class="xs-row__meta">
              <template v-if="p.last_run_at">Last ran <RelativeTime :at="p.last_run_at" />, on {{ EVENT_NOUNS[p.last_event] ?? p.last_event }}</template>
              <template v-else>Has not run yet</template>
            </p>
          </div>
          <UiButton
            size="sm"
            :aria-expanded="openPlugin === p.name ? 'true' : 'false'"
            :aria-controls="openPlugin === p.name ? pluginDetailId : undefined"
            @click="openPlugin = openPlugin === p.name ? '' : p.name"
          >
            {{ openPlugin === p.name ? 'Close' : 'Edit' }}<span class="sr-only"> {{ p.name }}</span>
          </UiButton>
        </li>
      </ul>
      <div>
        <UiButton>Install plugin…</UiButton>
      </div>

      <div v-if="openPlugin" :id="pluginDetailId" class="xs-editor">
        <h3 class="xs-editor__title">Plugin {{ visibleText(plugin.name) }}</h3>
        <Callout v-if="plugin.state === 'error'" tone="danger" title="The last run failed">
          <p>{{ visibleText(plugin.message) }}</p>
        </Callout>
        <Toggle v-model="pluginOn[plugin.name]!" label="Runs its hooks" />
        <dl class="xs-facts">
          <dt>Source</dt>
          <dd>{{ visibleText(plugin.source) }}</dd>
          <dt>Version</dt>
          <dd>
            {{ visibleText(plugin.version) }} at <code>{{ short(plugin.commit) }}</code>
          </dd>
          <dt>Hooks</dt>
          <dd>
            <ul class="xs-hooks">
              <li v-for="(h, i) in plugin.hooks" :key="i">{{ hookWords(h) }}</li>
            </ul>
          </dd>
        </dl>
        <div v-if="globalActions.length" class="xs-actions">
          <UiButton v-for="a in globalActions" :key="a.name" size="sm">{{ visibleText(a.description) }}</UiButton>
          <p class="xs-actions__result" role="status">{{ sampleActionResult }}</p>
        </div>
        <div class="xs-buttons">
          <UiButton size="sm">Update</UiButton>
          <UiButton size="sm" variant="danger">Remove {{ visibleText(plugin.name) }}…</UiButton>
        </div>

        <FormSection legend="Configuration" :level="4" description="A JSON object; the plugin reads it as its configuration.">
          <div class="xs-code"><TextArea v-model="configText" label="Configuration (JSON)" :rows="6" spellcheck="false" /></div>
        </FormSection>

        <div class="xs-log">
          <div class="xs-log__bar">
            <p class="xs-log__label">Log, newest last</p>
            <UiButton size="sm" variant="ghost">Refresh</UiButton>
          </div>
          <pre class="xs-log__text" tabindex="0" aria-label="Log of ticket-log">{{ visibleLines(logText) }}</pre>
        </div>

        <SaveBar :dirty="true" :busy="false" :label="`Save changes to the configuration of ${plugin.name}`" />
      </div>
    </FormSection>
  </template>
</template>

<style scoped>
.xs-pair {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(calc(var(--omo-space) * 50), 1fr));
  gap: calc(var(--omo-space) * 4);
}

/* Limits per window: the same table as the environment's, stacked when narrow. */
.xs-over {
  container-type: inline-size;
}
.xs-over__group {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.xs-over__head,
.xs-over__row {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 1fr) minmax(0, 1fr) var(--omo-control-h);
  align-items: center;
  column-gap: calc(var(--omo-space) * 2);
}
.xs-over__head {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-over__rows {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.xs-over__cell {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
/* Wide, the head names the columns; narrow, each field has its own small label. */
.xs-over__cell-label {
  display: none;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@container (max-width: 479.98px) {
  .xs-over__head {
    display: none;
  }
  .xs-over__row {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) var(--omo-control-h);
    align-items: end;
    row-gap: calc(var(--omo-space) * 2);
    padding-bottom: calc(var(--omo-space) * 2);
    border-bottom: var(--omo-border) solid var(--omo-line);
  }
  .xs-over__cell-label {
    display: block;
  }
  .xs-over__cell--name {
    grid-column: 1 / -1;
  }
}

/* A list of items, each with its own button; the open one is marked like a selected row. */
.xs-list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.xs-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
}
.xs-row--open {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.xs-row__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.xs-row p {
  margin: 0;
  overflow-wrap: anywhere;
}
.xs-row__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.xs-row__version,
.xs-row__meta {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-row__desc {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-row__error {
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-row > .xs-row__meta,
.xs-row > :deep(button) {
  flex-shrink: 0;
}

/* The open item: a framed box under the list, like a workspace folder. */
.xs-editor {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-bg);
}
.xs-editor__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
}
.xs-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
/* Scripts and configuration keep their lines; long ones scroll inside the box. */
.xs-code :deep(textarea) {
  white-space: pre;
  overflow-x: auto;
  overflow-wrap: normal;
}
.xs-ref {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.xs-ref summary {
  justify-self: start;
  color: var(--omo-muted);
  cursor: pointer;
}
.xs-ref__list {
  display: grid;
  grid-template-columns: minmax(0, max-content) minmax(0, 1fr);
  gap: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 4);
  margin: calc(var(--omo-space) * 2) 0 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-ref__list dt,
.xs-ref__list dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.xs-ref__list dd {
  color: var(--omo-muted);
}
.xs-test {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the select below its one-line label. */
.xs-test__run {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
}
.xs-result {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.xs-result__head {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-meters {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.xs-log {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.xs-log__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  padding-right: calc(var(--omo-space) * 1.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.xs-log__label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-log > .xs-log__label {
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.xs-log__text {
  max-height: calc(var(--omo-space) * 50);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: pre;
}
.xs-log__text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.xs-state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
}
.xs-state__text {
  flex: 1 1 calc(var(--omo-space) * 60);
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.xs-path {
  color: var(--omo-fg);
  overflow-wrap: anywhere;
}
.xs-facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 6);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.xs-facts dt {
  color: var(--omo-muted);
}
.xs-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.xs-hooks {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  margin: 0;
  padding: 0;
  list-style: none;
}
.xs-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
}
.xs-actions__result {
  flex: 1 1 calc(var(--omo-space) * 60);
  margin: 0;
  color: var(--omo-ok);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
