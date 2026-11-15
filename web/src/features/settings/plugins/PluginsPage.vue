<script setup lang="ts">
// The Plugins section of the settings: the installed plugins with their
// state, one open plugin at a time, "Install plugin…" and the plugins that
// come with omo. The list is the app's shared plugin list (the ticket panel's
// plugin actions come from it too): read again when this page opens, kept
// current by `plugin.changed`, by changes of the plugin settings files, after
// a snapshot and when the event stream is live again. The open plugin is part
// of this page's state, not of the address.
import { computed, ref, useId } from 'vue'
import { installPlugin } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { PluginInfo } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { usePluginList } from '../../ticket/pluginActions'
import FileErrors from '../FileErrors.vue'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { LEAVE_QUESTION, askFirst } from '../useLeaveGuard'
import InstallDialog from './InstallDialog.vue'
import PluginDetail from './PluginDetail.vue'
import PluginList from './PluginList.vue'
import { bundledToOffer, fileOfPlugin } from './pluginForms'

const def = sectionDef('plugins')
const session = useSession()
const announce = useAnnouncer()
const list = usePluginList()
if (list.data.value !== null) void list.reload()

const data = computed(() => list.data.value)
const plugins = computed(() => data.value?.plugins ?? [])
const settings = computed(() => data.value?.settings ?? [])
const loaded = computed(() => data.value !== null)
const empty = computed(() => plugins.value.length === 0 && settings.value.length === 0)
const offered = computed(() => bundledToOffer(data.value?.bundled ?? [], settings.value))

const selected = ref<string | null>(null)
const detailDirty = ref(false)
const boxId = useId()
/** Broken settings files of plugins whose box is not open (the open box shows its own). */
const listErrors = computed(() => (data.value?.errors ?? []).filter((e) => fileOfPlugin(e.file) !== selected.value))
const openInfo = computed(() => plugins.value.find((p) => p.name === selected.value) ?? null)
const openSettings = computed(() => settings.value.find((s) => s.name === selected.value) ?? null)

async function leaveOpen(): Promise<boolean> {
  if (detailDirty.value && !(await askFirst(LEAVE_QUESTION))) return false
  detailDirty.value = false
  return true
}
async function toggle(name: string) {
  if (!(await leaveOpen())) return
  selected.value = selected.value === name ? null : name
}
function removed() {
  detailDirty.value = false
  selected.value = null
  void list.reload()
}

const installing = ref(false)
async function installed(info: PluginInfo) {
  list.take(info)
  void list.reload()
  if (await leaveOpen()) selected.value = info.name
}

// Bundled plugins install at once: they come with omo.
const bundledBusy = ref<string | null>(null)
const bundledError = ref<{ name: string; text: string } | null>(null)
async function installBundled(name: string) {
  if (bundledBusy.value !== null) return
  bundledBusy.value = name
  bundledError.value = null
  try {
    const { data: info } = await installPlugin(session.client, { name, source: `builtin:${name}`, subpath: '', ref: '', enabled: true, config: {} })
    announce.polite(`Plugin ${visibleText(name)} installed.`)
    await installed(info)
  } catch (e) {
    const text = failureText(e, { lost: 'No answer from omo; it may have been installed. Look in the list above.', refused: 'The install was refused.' })
    bundledError.value = { name, text }
    announce.assertive(visibleText(text))
  } finally {
    bundledBusy.value = null
  }
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <FormSection legend="Installed" description="Plugins react to what happens on the board. They run as you, with your rights.">
      <FileErrors :errors="listErrors" />
      <Callout v-if="list.error.value" tone="danger" live="assertive" data-plugins-error>
        <p>{{ visibleText(list.error.value) }}</p>
        <template #actions><UiButton size="sm" @click="list.reload()">Try again</UiButton></template>
      </Callout>
      <p v-if="!loaded && !list.error.value" class="plugins__note">Reading the plugins…</p>
      <p v-else-if="loaded && empty" class="plugins__note" data-no-plugins>No plugin installed.</p>
      <PluginList v-else-if="loaded" :plugins="plugins" :settings="settings" :selected="selected" :controls="boxId" @select="toggle" />
      <div>
        <UiButton data-install-plugin @click="installing = true">Install plugin…</UiButton>
      </div>

      <PluginDetail
        v-if="selected !== null"
        :key="selected"
        :name="selected"
        :info="openInfo"
        :settings="openSettings"
        :box-id="boxId"
        @dirty="(d) => (detailDirty = d)"
        @info="list.take"
        @removed="removed"
        @reload="list.reload()"
      />
    </FormSection>

    <FormSection v-if="loaded" legend="Bundled" description="Plugins that come with omo. Installing copies one into your plugins.">
      <p v-if="offered.length === 0" class="plugins__note" data-no-bundled>Every plugin that comes with omo is installed.</p>
      <ul v-else class="plugins__bundled">
        <li v-for="b in offered" :key="b.name" class="plugins__row" :data-bundled="b.name">
          <div class="plugins__main">
            <p class="plugins__name">{{ visibleText(b.name) }}</p>
            <p class="plugins__desc">{{ visibleText(b.description) }}</p>
            <p v-if="bundledError?.name === b.name" class="plugins__error" role="alert" data-bundled-error>{{ visibleText(bundledError.text) }}</p>
          </div>
          <UiButton size="sm" :disabled="bundledBusy !== null" :aria-busy="bundledBusy === b.name ? 'true' : undefined" data-install-bundled @click="installBundled(b.name)">
            {{ bundledBusy === b.name ? 'Installing…' : 'Install' }}<span class="sr-only"> {{ visibleText(b.name) }}</span>
          </UiButton>
        </li>
      </ul>
    </FormSection>

    <InstallDialog :open="installing" @close="installing = false" @installed="installed" />
  </SettingsPage>
</template>

<style scoped>
.plugins__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.plugins__bundled {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.plugins__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.plugins__row > :deep(button) {
  flex-shrink: 0;
}
.plugins__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.plugins__main p {
  margin: 0;
  overflow-wrap: anywhere;
}
.plugins__name {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.plugins__desc {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.plugins__error {
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
