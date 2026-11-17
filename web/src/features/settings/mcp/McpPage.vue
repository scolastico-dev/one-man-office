<script setup lang="ts">
// The MCP section of the settings: the servers omo relays (with their live
// state from mcp.changed and one open form at a time) and omo's general MCP
// switches, the agent programs omo registers itself in, the take-over of
// the servers those programs have, and the access of programs that are no
// omo agent session (personal tokens, OAuth grants). The list is read again
// when a server file changes, after a snapshot and when the event stream is
// live again; states arrive one by one with mcp.changed.
import { computed, onScopeDispose, ref, useId } from 'vue'
import { getMCPServers } from '../../../api/endpoints'
import type { ApiEvent, FileError, MCPServer, MCPServerState, SettingsChangedData, SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { liveRead } from '../../../store/liveRead'
import FileErrors from '../FileErrors.vue'
import NameDialog from '../NameDialog.vue'
import { focusAfterClose, rowButton, useOpenEditor } from '../openEditor'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import McpAccess from './McpAccess.vue'
import McpClients from './McpClients.vue'
import McpServerEditor from './McpServerEditor.vue'
import McpServerList from './McpServerList.vue'
import McpSwitches from './McpSwitches.vue'
import McpTakeover from './McpTakeover.vue'
import { blankServer, fileOfMCPServer, type McpDraft } from './mcpForms'

const def = sectionDef('mcp')
const session = useSession()
const store = useBoard()

const servers = ref<MCPServer[]>([])
const states = ref<MCPServerState[]>([])
const errors = ref<FileError[]>([])
const loaded = ref(false)
const loadError = ref('')
/** MCP is switched off in the stored general switches: omo serves no endpoint and runs no server. */
const off = ref(false)

function concernsServers(ev: ApiEvent): boolean {
  if (ev.kind === 'settings.changed') return (ev.data as Partial<SettingsChangedData> | null)?.section === 'mcpservers'
  if (ev.kind === 'settings.invalid') {
    const f = (ev.data as Partial<SettingsInvalidData> | null)?.file
    return typeof f === 'string' && fileOfMCPServer(f) !== null
  }
  return false
}
const list = liveRead({
  store,
  read: () => getMCPServers(session.client),
  apply({ data }) {
    servers.value = data.servers ?? []
    states.value = data.states ?? []
    errors.value = data.errors ?? []
    loadError.value = ''
    loaded.value = true
  },
  failed(e) {
    loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The MCP servers could not be read.'
    loaded.value = true
  },
  watches: concernsServers,
})
const reload = () => list.reload()

// A state change of one server comes with its new state: no read needed.
onScopeDispose(
  store.onEvent('mcp.changed', (ev) => {
    const st = ev.data as MCPServerState | null
    if (st === null || typeof st.name !== 'string') return
    states.value = [...states.value.filter((s) => s.name !== st.name), st]
  }),
)

const selected = ref<string | null>(null)
const initial = ref<McpDraft | null>(null)
const editor = useOpenEditor(() => `the MCP server ${visibleText(selected.value ?? '')}`)
const formId = useId()

const names = computed(() => servers.value.map((s) => s.name))
const listErrors = computed(() => errors.value.filter((e) => fileOfMCPServer(e.file) !== selected.value))
const stateOfOpen = computed(() => states.value.find((s) => s.name === selected.value) ?? null)

async function toggle(name: string) {
  if (!(await editor.leave())) return
  selected.value = selected.value === name ? null : name
  initial.value = null
}
async function openNew(name: string, start: McpDraft): Promise<boolean> {
  if (!(await editor.leave())) return false
  selected.value = name
  initial.value = start
  return true
}
const newButton = ref<InstanceType<typeof UiButton>>()
const focusAfter = (name: string) =>
  focusAfterClose(
    () => rowButton('data-mcp-server', name, 'data-mcp-server-toggle'),
    () => newButton.value?.$el as HTMLElement | undefined,
  )
async function closeNew() {
  if (!(await editor.leave())) return
  const name = selected.value
  selected.value = null
  initial.value = null
  if (name !== null) await focusAfter(name)
}
async function deleted() {
  const name = selected.value
  editor.reset()
  selected.value = null
  initial.value = null
  await reload()
  if (name !== null && selected.value === null) await focusAfter(name)
}
/** Servers taken over from the agent programs: read the list again at once. */
const takenOver = () => void reload()

const naming = ref(false)
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <FormSection
      data-mcp-section="servers"
      legend="Servers"
      description="MCP servers omo starts or connects to and offers to the agent sessions it starts, with your credentials kept in omo. Each agent sees a server's tools under the server's name."
    >
      <FileErrors :errors="listErrors" />
      <Callout v-if="loadError" tone="danger" live="assertive" data-mcp-error>
        <p>{{ loadError }}</p>
        <template #actions><UiButton size="sm" @click="reload()">Try again</UiButton></template>
      </Callout>
      <Callout v-if="off" tone="warn" title="MCP is switched off" data-mcp-off>
        <p>
          omo serves no MCP endpoint, starts or connects to none of these servers and gives its agents no MCP entry. The settings are kept; the
          general switches below turn it on again.
        </p>
      </Callout>
      <p v-if="!loaded" class="mcp__note">Reading the MCP servers…</p>
      <p v-else-if="servers.length === 0 && !loadError" class="mcp__note" data-no-mcp-servers>No MCP server yet.</p>
      <McpServerList v-else :servers="servers" :states="states" :selected="selected" :controls="formId" @select="toggle" />
      <div class="mcp__buttons">
        <UiButton ref="newButton" data-new-mcp-server @click="naming = true">Add MCP server…</UiButton>
      </div>

      <Callout v-if="editor.busyNote.value" tone="warn" data-editor-busy>
        <p>{{ editor.busyNote.value }}</p>
      </Callout>
      <McpServerEditor
        v-if="selected !== null"
        :key="selected"
        :name="selected"
        :initial="initial"
        :state="stateOfOpen"
        :off="off"
        :form-id="formId"
        @dirty="(d) => (editor.dirty.value = d)"
        @busy="(b) => (editor.busy.value = b)"
        @deleted="deleted"
        @close="closeNew"
      />

      <McpSwitches @enabled="(on) => (off = !on)" />
    </FormSection>

    <McpClients />
    <McpTakeover :taken="names" @taken-over="takenOver" />
    <McpAccess />

    <NameDialog
      :open="naming"
      :taken="names"
      title="Add MCP server"
      eyebrow="MCP"
      thing="MCP server"
      confirm="Add"
      @close="naming = false"
      @create="(n) => openNew(n, blankServer(n))"
    />
  </SettingsPage>
</template>

<style scoped>
.mcp__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.mcp__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
</style>
