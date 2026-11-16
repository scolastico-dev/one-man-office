<script setup lang="ts">
// The Plugins section of the settings: the installed plugins with their
// state, one open plugin at a time and "Install plugin…". The list is the app's shared plugin list (the ticket panel's
// plugin actions come from it too): read again when this page opens, kept
// current by `plugin.changed`, by changes of the plugin settings files, after
// a snapshot and when the event stream is live again. The open plugin is part
// of this page's state, not of the address.
import { computed, ref, useId } from 'vue'
import type { PluginInfo } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { usePluginList } from '../../ticket/pluginActions'
import FileErrors from '../FileErrors.vue'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { focusAfterClose, rowButton, useOpenEditor } from '../openEditor'
import InstallDialog from './InstallDialog.vue'
import PluginDetail from './PluginDetail.vue'
import PluginList from './PluginList.vue'
import { fileOfPlugin } from './pluginForms'

const def = sectionDef('plugins')
const list = usePluginList()
if (list.data.value !== null) void list.reload()

const data = computed(() => list.data.value)
const plugins = computed(() => data.value?.plugins ?? [])
const settings = computed(() => data.value?.settings ?? [])
const loaded = computed(() => data.value !== null)
const empty = computed(() => plugins.value.length === 0 && settings.value.length === 0)

const selected = ref<string | null>(null)
const editor = useOpenEditor(() => `the plugin ${visibleText(selected.value ?? '')}`)
const boxId = useId()
/** Broken settings files of plugins whose box is not open (the open box shows its own). */
const listErrors = computed(() => (data.value?.errors ?? []).filter((e) => fileOfPlugin(e.file) !== selected.value))
const openInfo = computed(() => plugins.value.find((p) => p.name === selected.value) ?? null)
const openSettings = computed(() => settings.value.find((s) => s.name === selected.value) ?? null)

async function toggle(name: string) {
  if (!(await editor.leave())) return
  selected.value = selected.value === name ? null : name
}
const installButton = ref<InstanceType<typeof UiButton>>()
async function removed() {
  const name = selected.value
  editor.reset()
  selected.value = null
  await list.reload()
  // Focus goes to the row when it is still listed, else to Install plugin….
  if (name !== null && selected.value === null) {
    await focusAfterClose(
      () => rowButton('data-plugin', name, 'data-plugin-toggle'),
      () => installButton.value?.$el as HTMLElement | undefined,
    )
  }
}

const installing = ref(false)
async function installed(info: PluginInfo) {
  list.take(info)
  void list.reload()
  if (await editor.leave()) selected.value = info.name
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
        <UiButton ref="installButton" data-install-plugin @click="installing = true">Install plugin…</UiButton>
      </div>

      <Callout v-if="editor.busyNote.value" tone="warn" data-editor-busy>
        <p>{{ editor.busyNote.value }}</p>
      </Callout>
      <PluginDetail
        v-if="selected !== null"
        :key="selected"
        :name="selected"
        :info="openInfo"
        :settings="openSettings"
        :box-id="boxId"
        @dirty="(d) => (editor.dirty.value = d)"
        @busy="(b) => (editor.busy.value = b)"
        @info="list.take"
        @removed="removed"
        @reload="list.reload()"
      />
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
</style>
