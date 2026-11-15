<script setup lang="ts">
// One plugin, opened under the list: its state and last error, whether its
// hooks run (saved at once, guarded by the settings file's revision), where
// it comes from and which commit is installed, its hooks in words, its
// global actions as buttons with their answer, Update (Install while its
// files are missing), Remove…, its configuration with its own Save bar and
// its log. Everything the plugin or the server wrote is shown as visible text.
// A plugin removed elsewhere while its box is open says so; its unsaved edits
// are dropped and nothing that needs the plugin is offered any more.
import { computed, onMounted, ref, useId, watch } from 'vue'
import { ApiError } from '../../../api/client'
import { deletePlugin, getPlugins, putPlugin, runPluginAction, updatePlugin } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { PluginInfo, PluginSettings } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { askFirst } from '../useLeaveGuard'
import PluginConfigEditor from './PluginConfigEditor.vue'
import PluginLog from './PluginLog.vue'
import { shortCommit } from '../extensionWords'
import { hookWords, installResult, manualActions, sameSettings } from './pluginForms'

const props = defineProps<{
  name: string
  /** What the runtime says of the plugin; null while it is not known. */
  info: PluginInfo | null
  /** Its settings file; null when there is none. */
  settings: PluginSettings | null
  /** The id of this box, which the list's button names in aria-controls. */
  boxId?: string
}>()
const emit = defineEmits<{
  /** Whether the configuration has unsaved edits. */
  dirty: [boolean]
  /** A newer state the server answered with. */
  info: [PluginInfo]
  /** The plugin was removed. */
  removed: []
  /** The list should be read again (a write that the events may not cover). */
  reload: []
  /** omo is working on the plugin for this box (switch, action, update, removal): the box stays open meanwhile. */
  busy: [boolean]
}>()

const session = useSession()
const announce = useAnnouncer()
const fallbackId = useId()

const shownName = computed(() => visibleText(props.name))
/** Neither the runtime nor a settings file knows the plugin any more: it was removed elsewhere. */
const gone = computed(() => props.info === null && props.settings === null)
watch(gone, (g) => {
  if (g) emit('dirty', false)
})
const state = computed(() => props.info?.state ?? 'not_installed')
const notInstalled = computed(() => state.value === 'not_installed')
const hooks = computed(() => props.info?.hooks ?? [])
const globalActions = computed(() => (props.info ? manualActions(props.info, 'global') : []))
const failure = computed(() => {
  const i = props.info
  if (i === null || i.message === '') return null
  if (i.state === 'error') return { tone: 'danger' as const, title: 'The last run failed' }
  if (i.state === 'invalid') return { tone: 'danger' as const, title: 'It does not load' }
  return null
})

const heading = ref<HTMLElement>()
onMounted(() => heading.value?.focus())

// Enabled: saved at once

const on = ref(props.settings?.enabled ?? false)
const switching = ref(false)
const switchError = ref('')
watch(
  () => props.settings?.enabled,
  (v) => {
    if (!switching.value && v !== undefined) on.value = v
  },
)

/** The plugin's settings as stored now when they differ from `s` in their revision only (another write came first); else null. */
async function onlyRevisionChanged(s: PluginSettings): Promise<PluginSettings | null> {
  try {
    const { data } = await getPlugins(session.client)
    const fresh = data.settings.find((x) => x.name === props.name)
    return fresh !== undefined && sameSettings(fresh, s) ? fresh : null
  } catch {
    return null
  }
}

async function setEnabled(v: boolean) {
  const s = props.settings
  if (s === null || switching.value) return
  on.value = v
  switching.value = true
  switchError.value = ''
  const send = (base: PluginSettings) => putPlugin(session.client, { ...base, revision: undefined, enabled: v }, base.revision ?? '')
  try {
    let answer: Awaited<ReturnType<typeof send>>
    try {
      answer = await send(s)
    } catch (e) {
      // Only the revision was stale (a write right before, such as another
      // switch): read again and send once more; a real change is shown.
      if (!(e instanceof ApiError && e.code === 'stale_settings')) throw e
      const fresh = await onlyRevisionChanged(s)
      if (fresh === null) throw e
      answer = await send(fresh)
    }
    const { data } = answer
    emit('info', data)
    announce.polite(`${shownName.value} ${v ? 'runs its hooks' : 'is off'}.`)
  } catch (e) {
    on.value = s.enabled
    switchError.value =
      e instanceof ApiError && e.code === 'stale_settings'
        ? `${e.message}. The page shows the new settings; switch again if you still want to.`
        : failureText(e, { lost: 'No answer from omo; the switch may have been saved. The list shows what omo stored.', refused: 'The change was refused.' })
    announce.assertive(visibleText(switchError.value))
    emit('reload')
  } finally {
    switching.value = false
  }
}

// Global actions

const running = ref<string | null>(null)
const answer = ref('')
const actionError = ref('')

async function runAction(action: string, description: string) {
  if (running.value !== null) return
  running.value = action
  answer.value = ''
  actionError.value = ''
  try {
    const { data } = await runPluginAction(session.client, props.name, action)
    const text = data?.message ?? ''
    answer.value = text === '' ? `${description}: done; the plugin gave no answer.` : text
    announce.polite(visibleText(answer.value))
  } catch (e) {
    actionError.value = failureText(e, { lost: 'No answer from omo; the action may have run. Its log says.', refused: 'The action was refused.' })
    announce.assertive(visibleText(actionError.value))
  } finally {
    running.value = null
  }
}

// Update or install

const updating = ref(false)
const updated = ref<{ ok: boolean; text: string } | null>(null)
const updateError = ref('')
async function update() {
  if (updating.value) return
  const before = !notInstalled.value
  updating.value = true
  updated.value = null
  updateError.value = ''
  try {
    const { data } = await updatePlugin(session.client, props.name)
    emit('info', data)
    updated.value = installResult(data, before)
    announce.polite(visibleText(updated.value.text))
  } catch (e) {
    updateError.value = failureText(e, { lost: 'No answer from omo; it may still be fetching. The list shows the state.', refused: 'It was refused.' })
    announce.assertive(visibleText(updateError.value))
  } finally {
    updating.value = false
  }
}

// Remove

const removing = ref(false)
const removeError = ref('')

watch(
  () => switching.value || running.value !== null || updating.value || removing.value,
  (b) => emit('busy', b),
)
async function remove() {
  const ok = await askFirst({
    title: `Remove the plugin ${shownName.value}?`,
    text: 'omo stops its hooks and deletes its files, its configuration and everything it stored, its log included. This cannot be undone.',
    confirm: 'Remove',
    cancel: 'Keep it',
  })
  if (!ok) return
  removing.value = true
  removeError.value = ''
  try {
    await deletePlugin(session.client, props.name)
    announce.polite(`Plugin ${shownName.value} removed.`)
    emit('removed')
  } catch (e) {
    removeError.value = failureText(e, { lost: 'No answer from omo, so the plugin may have been removed. Look in the list.', refused: 'The plugin could not be removed.' })
    announce.assertive(visibleText(removeError.value))
  } finally {
    removing.value = false
  }
}
</script>

<template>
  <section :id="boxId ?? fallbackId" class="plugin-detail" :aria-labelledby="`${fallbackId}-title`" :data-plugin-detail="name">
    <h3 :id="`${fallbackId}-title`" ref="heading" class="plugin-detail__title" tabindex="-1">Plugin {{ shownName }}</h3>

    <Callout v-if="gone" tone="warn" live="polite" data-plugin-gone>
      <p>The plugin {{ shownName }} no longer exists.</p>
    </Callout>
    <template v-if="!gone">
      <Callout v-if="failure" :tone="failure.tone" :title="failure.title" data-plugin-failure>
        <p>{{ visibleText(info?.message ?? '') }}</p>
      </Callout>
      <Callout v-if="notInstalled && settings" tone="warn" title="Not installed" data-plugin-missing>
        <p>Its settings are here, its files are not. Install fetches it from its source.</p>
      </Callout>

      <div v-if="settings" class="plugin-detail__switch">
        <Toggle :model-value="on" label="Runs its hooks" :disabled="switching" data-plugin-enabled @update:model-value="setEnabled" />
        <Callout v-if="switchError" tone="danger" data-plugin-switch-error>
        <p>{{ visibleText(switchError) }}</p>
      </Callout>
      </div>

      <dl class="plugin-detail__facts">
        <template v-if="settings">
          <dt>Source</dt>
          <dd data-plugin-source>{{ visibleText(settings.source) }}</dd>
          <template v-if="settings.subpath">
            <dt>Folder</dt>
            <dd>{{ visibleText(settings.subpath) }}</dd>
          </template>
          <template v-if="settings.ref">
            <dt>Branch or tag</dt>
            <dd>{{ visibleText(settings.ref) }}</dd>
          </template>
        </template>
        <template v-if="info && !notInstalled">
          <dt>Version</dt>
          <dd data-plugin-version>
            {{ visibleText(info.version || 'no version') }}<template v-if="info.commit === 'bundled'">, bundled with omo</template
            ><template v-else-if="info.commit"> at <code>{{ shortCommit(info.commit) }}</code></template>
          </dd>
        </template>
        <template v-if="hooks.length">
          <dt>Hooks</dt>
          <dd>
            <ul class="plugin-detail__hooks">
              <li v-for="(h, i) in hooks" :key="i">{{ visibleText(hookWords(h)) }}</li>
            </ul>
          </dd>
        </template>
      </dl>

      <div v-if="globalActions.length" class="plugin-detail__actions">
        <UiButton
          v-for="a in globalActions"
          :key="a.name"
          size="sm"
          :disabled="running !== null || state !== 'ready' && state !== 'error' && state !== 'running'"
          :aria-busy="running === a.name ? 'true' : undefined"
          :data-plugin-action="a.name"
          @click="runAction(a.name, a.description)"
        >
          {{ visibleText(a.description) }}{{ running === a.name ? '…' : '' }}
        </UiButton>
        <p class="plugin-detail__result" data-plugin-answer>{{ visibleText(answer) }}</p>
      </div>
      <Callout v-if="actionError" tone="danger" data-plugin-action-error>
        <p>{{ visibleText(actionError) }}</p>
      </Callout>

      <div class="plugin-detail__buttons">
        <UiButton v-if="settings" size="sm" :disabled="updating" :aria-busy="updating ? 'true' : undefined" data-plugin-update @click="update">
          {{ updating ? (notInstalled ? 'Installing…' : 'Updating…') : notInstalled ? 'Install' : 'Update' }}
        </UiButton>
        <UiButton size="sm" variant="danger" :disabled="removing" data-plugin-remove @click="remove">Remove {{ shownName }}…</UiButton>
      </div>
      <p v-if="updated" class="plugin-detail__result" :class="{ 'plugin-detail__result--bad': !updated.ok }" data-plugin-update-result>
        {{ visibleText(updated.text) }}
      </p>
      <Callout v-if="updateError" tone="danger" data-plugin-update-error>
        <p>{{ visibleText(updateError) }}</p>
      </Callout>
      <Callout v-if="removeError" tone="danger" data-plugin-remove-error>
        <p>{{ visibleText(removeError) }}</p>
      </Callout>

      <PluginConfigEditor v-if="settings" :name="name" @dirty="(d) => emit('dirty', d)" @info="(i) => emit('info', i)">
        <PluginLog :name="name" :last-run-at="info?.last_run_at ?? null" />
      </PluginConfigEditor>
      <PluginLog v-else :name="name" :last-run-at="info?.last_run_at ?? null" />
    </template>
  </section>
</template>

<style scoped>
/* The open plugin: a framed box under the list, like a workspace folder. */
.plugin-detail {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-bg);
}
.plugin-detail__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
  overflow-wrap: anywhere;
}
.plugin-detail__title:focus-visible {
  outline-offset: var(--omo-space);
}
.plugin-detail__switch {
  display: grid;
  gap: var(--omo-space);
}
.plugin-detail__facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 6);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.plugin-detail__facts dt {
  color: var(--omo-muted);
}
.plugin-detail__facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.plugin-detail__hooks {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  margin: 0;
  padding: 0;
  list-style: none;
}
.plugin-detail__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
}
.plugin-detail__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.plugin-detail__result {
  flex: 1 1 calc(var(--omo-space) * 60);
  margin: 0;
  color: var(--omo-ok);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.plugin-detail__result--bad {
  color: var(--omo-danger);
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
