<script setup lang="ts">
// One relayed MCP server's form: what it is (a program omo starts or an
// address), how omo signs in at it, which agent roles get it and whether
// connections without an omo session get it too, which of its tools are
// offered, and its time limits. It has its own resource (loaded with its
// revision, saved with If-Match, kept current while open) and its own Save
// bar. Test starts the draft once without saving it; Restart, Log and
// Connect/Disconnect act on the stored server. Values are shown as stored
// and never put into an address, a log line or a toast. Every text the
// server or the relay wrote is shown as visible text.
import { computed, onMounted, ref, useId, watch } from 'vue'
import { connectMCPServer, deleteMCPServer, disconnectMCPServer, getMCPServers, putMCPServer, restartMCPServer, testMCPServer } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import { NO_REVISION, type ApiEvent, type MCPServerState, type MCPTestResult, type SettingsChangedData, type SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import Chip from '../../../components/ui/Chip.vue'
import DurationField from '../../../components/ui/DurationField.vue'
import EnvTable from '../../../components/ui/EnvTable.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
import RelativeTime from '../../../components/ui/RelativeTime.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import FileErrors from '../FileErrors.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { askFirst, useLeaveGuard } from '../useLeaveGuard'
import { diskChangeText, focusFirstInvalid, useSettingsResource } from '../useSettingsResource'
import McpServerLog from './McpServerLog.vue'
import {
  AUTH_OPTIONS,
  GLOBAL_HINT,
  GLOBAL_LABEL,
  INSTANCE_OPTIONS,
  MCP_ROLES,
  TRANSPORT_OPTIONS,
  errorsOfMCPServer,
  fileOfMCPServer,
  isPlacedField,
  listErrors,
  mapErrors,
  rolesError,
  stateWords,
  toDraft,
  toServer,
  toggleRole,
  type McpDraft,
} from './mcpForms'
import type { SkillRole } from '../skills/skillForms'

const props = withDefaults(
  defineProps<{
    name: string
    /** For a server not stored yet: where its form starts. */
    initial?: McpDraft | null
    /** What the relay says of the server; absent until told. */
    state?: MCPServerState | null
    /** MCP is switched off: omo runs no server, so its state, Test, Restart, Log and Connect are not offered. */
    off?: boolean
    /** The id of this form, which the list's button names in aria-controls. */
    formId?: string
  }>(),
  { initial: null, state: null, off: false, formId: undefined },
)
const emit = defineEmits<{
  dirty: [boolean]
  deleted: []
  close: []
  /** omo is working on the server for this form (Test, Restart, Connect, Delete). */
  busy: [boolean]
}>()

const session = useSession()
const announce = useAnnouncer()
const uid = useId()

let wasStored = false
const stored = ref(false)
const gone = ref(false)
const shownName = computed(() => visibleText(props.name))
const goneText = computed(() => `The MCP server ${shownName.value} no longer exists.`)

const r = useSettingsResource<McpDraft>({
  async load() {
    const { data } = await getMCPServers(session.client)
    const errors = errorsOfMCPServer(data.errors ?? [], props.name)
    const found = (data.servers ?? []).find((x) => x.name === props.name)
    if (found !== undefined) {
      wasStored = true
      stored.value = true
      gone.value = false
      return { value: toDraft(found), revision: found.revision ?? NO_REVISION, errors }
    }
    stored.value = false
    if (props.initial !== null && !wasStored) return { value: JSON.parse(JSON.stringify(props.initial)) as McpDraft, revision: NO_REVISION, errors, unsaved: true }
    gone.value = true
    throw new Error(goneText.value)
  },
  async save(draft, revision) {
    const { data, eventId } = await putMCPServer(session.client, toServer(draft), revision)
    wasStored = true
    stored.value = true
    gone.value = false
    return { value: toDraft(data), revision: data.revision ?? NO_REVISION, eventId }
  },
  watches(ev: ApiEvent) {
    if (ev.kind === 'settings.changed') {
      const d = ev.data as Partial<SettingsChangedData> | null
      return d?.section === 'mcpservers' && d.name === props.name
    }
    if (ev.kind === 'settings.invalid') {
      const d = ev.data as Partial<SettingsInvalidData> | null
      return typeof d?.file === 'string' && fileOfMCPServer(d.file) === props.name
    }
    return false
  },
})

useLeaveGuard(r.dirty)
watch(r.dirty, (d) => emit('dirty', d), { immediate: true })

const root = ref<HTMLElement>()
const heading = ref<HTMLElement>()
onMounted(() => heading.value?.focus())

const draft = computed(() => r.draft.value)
const fe = computed(() => r.fieldErrors.value)
const unplaced = computed(() => unplacedErrors(fe.value, isPlacedField))
const stdio = computed(() => draft.value?.transport === 'stdio')
const rolesId = `${uid}-roles`
const rolesHintId = `${uid}-roles-hint`
const rolesErrorId = `${uid}-roles-error`
const rolesRefusal = computed(() => rolesError(fe.value))
function flip(role: SkillRole) {
  if (draft.value) draft.value.roles = toggleRole(draft.value, role)
}
const label = computed(() => `Save changes to the MCP server ${shownName.value}`)
const st = computed(() => props.state)
const words = computed(() => (st.value ? stateWords(st.value.state) : null))
/** OAuth is stored for this server: Connect and Disconnect apply. */
const storedOAuth = ref(false)
watch(
  () => [stored.value, r.value.value?.auth, r.value.value?.transport] as const,
  ([s, auth, transport]) => (storedOAuth.value = s && auth === 'oauth' && transport !== 'stdio'),
  { immediate: true },
)
const needsLogin = computed(() => st.value?.state === 'needs_login' || st.value?.auth.state === 'needs_login')

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}

/** A refusal of the relay. */
function relayFailure(e: unknown, what: string): string {
  return failureText(e, { lost: `No answer from omo; the ${what} may still be running. Look at the state above.`, refused: `The ${what} was refused.` })
}

// Test

const testing = ref(false)
const tested = ref<MCPTestResult | null>(null)
const testError = ref('')
async function test() {
  if (!draft.value || testing.value) return
  testing.value = true
  tested.value = null
  testError.value = ''
  try {
    const { data } = await testMCPServer(session.client, toServer(draft.value))
    tested.value = data
    announce.polite(data.error ? `The test of ${shownName.value} failed.` : `The test of ${shownName.value} found ${data.tools.length} tools.`)
  } catch (e) {
    testError.value = relayFailure(e, 'test')
    announce.assertive(visibleText(testError.value))
  } finally {
    testing.value = false
  }
}

// Restart

const restarting = ref(false)
const actionNote = ref('')
const actionError = ref('')
async function restart() {
  restarting.value = true
  actionNote.value = ''
  actionError.value = ''
  try {
    await restartMCPServer(session.client, props.name)
    actionNote.value = `omo restarts ${shownName.value}; its state above follows.`
    announce.polite(actionNote.value)
  } catch (e) {
    actionError.value = relayFailure(e, 'restart')
    announce.assertive(visibleText(actionError.value))
  } finally {
    restarting.value = false
  }
}

// Connect / Disconnect

const connecting = ref(false)
/** The sign-in page's address, kept as a link when the browser blocked the new tab. */
const signIn = ref('')
async function connect() {
  connecting.value = true
  actionNote.value = ''
  actionError.value = ''
  signIn.value = ''
  try {
    const { data } = await connectMCPServer(session.client, props.name)
    const tab = window.open(data.authorize_url, '_blank', 'noopener')
    signIn.value = data.authorize_url
    actionNote.value = tab
      ? `The sign-in at ${shownName.value} opened in a new tab. Its state above changes once you signed in there.`
      : 'The browser blocked the new tab; open the sign-in page with the link below.'
    announce.polite(actionNote.value)
  } catch (e) {
    actionError.value = relayFailure(e, 'sign-in')
    announce.assertive(visibleText(actionError.value))
  } finally {
    connecting.value = false
  }
}
async function disconnect() {
  const ok = await askFirst({
    title: `Disconnect from ${shownName.value}?`,
    text: 'omo forgets its sign-in at this server, and its tools are left out until you connect again.',
    confirm: 'Disconnect',
    cancel: 'Stay connected',
  })
  if (!ok) return
  connecting.value = true
  actionNote.value = ''
  actionError.value = ''
  signIn.value = ''
  try {
    await disconnectMCPServer(session.client, props.name)
    actionNote.value = `omo is no longer signed in at ${shownName.value}.`
    announce.polite(actionNote.value)
  } catch (e) {
    actionError.value = relayFailure(e, 'sign-out')
    announce.assertive(visibleText(actionError.value))
  } finally {
    connecting.value = false
  }
}

// Log

const logShown = ref(false)
const logId = `${uid}-log`

// Delete

const deleteError = ref('')
const deleting = ref(false)
watch(
  () => testing.value || restarting.value || connecting.value || deleting.value,
  (b) => emit('busy', b),
)
async function remove() {
  const ok = await askFirst({
    title: `Delete the MCP server ${shownName.value}?`,
    text: 'omo removes its settings and stops it. Agents no longer get its tools; omo forgets its sign-in at it.',
    confirm: 'Delete',
    cancel: 'Keep it',
  })
  if (!ok) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deleteMCPServer(session.client, props.name, r.revision.value ?? NO_REVISION)
    announce.polite(`MCP server ${shownName.value} deleted.`)
    emit('deleted')
  } catch (e) {
    deleteError.value = failureText(e, { lost: 'No answer from omo, so the server may have been deleted. Look in the list.', refused: 'The server could not be deleted.' })
    announce.assertive(visibleText(deleteError.value))
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <section :id="formId ?? uid" ref="root" class="mcp-editor" :aria-labelledby="`${uid}-title`" :data-mcp-server-editor="name">
    <div class="mcp-editor__head">
      <h3 :id="`${uid}-title`" ref="heading" class="mcp-editor__title" tabindex="-1">
        MCP server {{ shownName }}<span v-if="!stored && !r.loading.value && !gone" class="mcp-editor__new"> (not saved yet)</span>
      </h3>
      <UiButton v-if="!stored" size="sm" data-mcp-server-close @click="emit('close')">Close</UiButton>
    </div>

    <FileErrors :errors="r.broken.value" />
    <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
      <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
    </Callout>
    <Callout v-if="gone" tone="warn" live="polite" data-mcp-server-gone>
      <p>{{ goneText }}</p>
    </Callout>
    <p v-if="r.loading.value && !draft" class="mcp-editor__note">Reading the server…</p>
    <Callout v-else-if="!draft && r.error.value && !gone" tone="danger" live="assertive" data-settings-load-error>
      <p>{{ r.error.value }}</p>
      <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
    </Callout>

    <template v-if="draft && !(gone && !r.dirty.value)">
      <UnplacedErrors :errors="unplaced" />

      <div v-if="stored && !off" class="mcp-editor__state" data-mcp-server-status>
        <div class="mcp-editor__state-text">
          <p>
            <template v-if="st && words">
              <span>State: {{ words.text }}</span>
              <template v-if="st.state === 'running' && st.started_at">, since <RelativeTime :at="st.started_at" /></template>
              <template v-if="st.instances > 1">, {{ st.instances }} processes</template>
            </template>
            <template v-else>omo has not told this server's state yet.</template>
          </p>
          <p v-if="needsLogin" class="mcp-editor__warn" data-mcp-needs-login>
            Needs login: omo is not signed in at this server, so its tools are left out. Connect signs in once in a new tab.
          </p>
          <p v-if="st?.auth.state === 'connected'" data-mcp-connected>
            Signed in<template v-if="st.auth.expires_at">, until <RelativeTime :at="st.auth.expires_at" /></template
            ><template v-if="st.auth.scopes.length > 0"> (scopes {{ st.auth.scopes.map(visibleText).join(', ') }})</template>.
          </p>
          <p v-if="st?.last_error" class="mcp-editor__failure" data-mcp-last-error>{{ visibleText(st.last_error) }}</p>
          <p v-if="st && st.tools.length > 0" data-mcp-tools>Tools: {{ st.tools.map(visibleText).join(', ') }}</p>
          <p v-for="o in st?.omitted ?? []" :key="o.tool" data-mcp-omitted>Left out: {{ visibleText(o.tool) }} ({{ visibleText(o.reason) }})</p>
          <p v-if="st?.offers_resources || st?.offers_prompts">This server also offers {{ st.offers_resources && st.offers_prompts ? 'resources and prompts' : st.offers_resources ? 'resources' : 'prompts' }}, which omo does not relay.</p>
        </div>
        <div class="mcp-editor__actions">
          <UiButton size="sm" :disabled="restarting" :aria-busy="restarting ? 'true' : undefined" data-mcp-restart @click="restart">
            {{ restarting ? 'Restarting…' : 'Restart' }}
          </UiButton>
          <UiButton v-if="storedOAuth && st?.auth.state !== 'connected'" size="sm" variant="primary" :disabled="connecting" data-mcp-connect @click="connect">Connect</UiButton>
          <UiButton v-if="storedOAuth && st?.auth.state === 'connected'" size="sm" :disabled="connecting" data-mcp-disconnect @click="disconnect">Disconnect…</UiButton>
          <UiButton size="sm" variant="ghost" :aria-expanded="logShown ? 'true' : 'false'" :aria-controls="logShown ? logId : undefined" data-mcp-log-toggle @click="logShown = !logShown">
            {{ logShown ? 'Hide log' : 'Log' }}
          </UiButton>
        </div>
      </div>
      <p v-if="actionNote" class="mcp-editor__result" data-mcp-action-note>
        {{ visibleText(actionNote) }}
        <a v-if="signIn" :href="signIn" target="_blank" rel="noopener noreferrer" data-mcp-sign-in-link>Open the sign-in page</a>
      </p>
      <Callout v-if="actionError" tone="danger" data-mcp-action-error>
        <p>{{ visibleText(actionError) }}</p>
      </Callout>
      <div v-if="logShown && stored" :id="logId"><McpServerLog :name="name" :changed="st?.state ?? null" /></div>

      <Toggle v-model="draft.enabled" label="omo relays this server" data-mcp-enabled-server />
      <TextField v-model="draft.description" label="Description" hint="One line for this page; optional." :error="fe['description']" autocomplete="off" />
      <SelectField v-model="draft.transport" label="Kind" :options="TRANSPORT_OPTIONS" :error="fe['transport']" data-mcp-transport />

      <template v-if="stdio">
        <TextField
          v-model="draft.command"
          label="Command"
          hint="The program, e.g. npx; found through omo's PATH."
          :error="fe['command']"
          spellcheck="false"
          autocomplete="off"
          autocapitalize="off"
          data-mcp-command
        />
        <ListField v-model="draft.args" label="Arguments" item-label="Argument" add-label="Add argument" empty-text="None." :error="fe['args']" :errors="listErrors(fe, 'args')" data-mcp-args />
        <EnvTable
          v-model="draft.env"
          label="Environment"
          hint="Plain values, as stored. ${VAR} and ${VAR:-default} take a value from omo's environment when the server starts."
          :errors="mapErrors(fe, 'env')"
          :error="fe['env']"
          data-mcp-env
        />
        <SelectField v-model="draft.instances" label="Processes" :options="INSTANCE_OPTIONS" :error="fe['instances']" />
        <TextField
          v-model="draft.cwd"
          label="Working folder"
          hint="Empty: the folder of the session (its worktree), or your home folder for connections without an omo session. Else an absolute path."
          :error="fe['cwd']"
          spellcheck="false"
          autocomplete="off"
          autocapitalize="off"
        />
      </template>
      <template v-else>
        <TextField v-model="draft.url" label="Address" hint="http:// or https://, e.g. https://mcp.example.com/mcp" :error="fe['url']" spellcheck="false" autocomplete="off" autocapitalize="off" data-mcp-url />
        <SelectField v-model="draft.auth" label="Authorization" :options="AUTH_OPTIONS" :error="fe['auth']" data-mcp-auth />
        <template v-if="draft.auth === 'headers'">
          <EnvTable
            v-model="draft.headers"
            label="Headers"
            hint="Sent with every request, values as stored, e.g. Authorization: Bearer ${EXAMPLE_TOKEN}. ${VAR} takes a value from omo's environment."
            :errors="mapErrors(fe, 'headers')"
            :error="fe['headers']"
            data-mcp-headers
          />
          <ListField
            v-model="draft.headersCommand"
            label="Headers command"
            item-label="Word"
            add-label="Add word"
            empty-text="None."
            hint="Optional: a program and its arguments that print a JSON object of headers, run before omo connects (for tokens that change)."
            :error="fe['headers_command']"
            :errors="listErrors(fe, 'headers_command')"
          />
        </template>
        <template v-if="draft.auth === 'oauth'">
          <p class="mcp-editor__note">
            omo signs in at the server's provider itself; after saving, Connect opens the sign-in. Leave the client id empty to let omo register itself.
            A client you registered with the provider needs omo's return address, <code>/oauth/callback</code> on the address you reach omo by.
          </p>
          <TextField v-model="draft.clientId" label="Client id" hint="Optional." :error="fe['oauth.client_id']" spellcheck="false" autocomplete="off" autocapitalize="off" />
          <TextField v-model="draft.clientSecret" label="Client secret" hint="Optional; only with a client id." :error="fe['oauth.client_secret']" spellcheck="false" autocomplete="off" autocapitalize="off" />
          <ListField v-model="draft.scopes" label="Scopes" item-label="Scope" add-label="Add scope" empty-text="None: what the server asks for." :error="fe['oauth.scopes']" :errors="listErrors(fe, 'oauth.scopes')" />
          <TextField v-model="draft.resource" label="Resource" hint="Optional: overrides the resource omo asks a token for." :error="fe['oauth.resource']" spellcheck="false" autocomplete="off" autocapitalize="off" />
        </template>
      </template>

      <template v-if="draft">
      <FormSection legend="Who gets it" :level="4" description="Agents omo starts with one of the chosen roles see the server's tools.">
        <div class="omo-field">
          <p :id="rolesId" class="omo-field__label">Roles</p>
          <div
            role="group"
            class="mcp-editor__roles"
            :aria-labelledby="rolesId"
            :aria-describedby="[rolesHintId, rolesRefusal ? rolesErrorId : ''].filter(Boolean).join(' ')"
            data-mcp-roles
          >
            <Chip
              v-for="role in MCP_ROLES"
              :key="role.id"
              as="button"
              :label="role.label"
              :pressed="draft.roles.includes(role.id)"
              :data-mcp-role="role.id"
              @click="flip(role.id)"
            />
          </div>
          <p :id="rolesHintId" class="omo-field__hint">
            {{ draft.roles.length === 0 ? 'None chosen: every role gets this server.' : 'Only the chosen roles get this server.' }}
          </p>
          <p v-if="rolesRefusal" :id="rolesErrorId" class="omo-field__error">{{ rolesRefusal }}</p>
        </div>
        <div class="mcp-editor__global">
          <Toggle v-model="draft.global" :label="GLOBAL_LABEL" data-mcp-global />
          <p class="omo-field__hint">{{ GLOBAL_HINT }}</p>
        </div>
      </FormSection>

      <FormSection legend="Tools and time limits" :level="4" description="Tool names as the server gives them.">
        <ListField v-model="draft.allow" label="Only these tools" item-label="Tool" add-label="Add tool" empty-text="None: every tool." :error="fe['tools.allow']" :errors="listErrors(fe, 'tools.allow')" />
        <ListField v-model="draft.deny" label="Never these tools" item-label="Tool" add-label="Add tool" empty-text="None." :error="fe['tools.deny']" :errors="listErrors(fe, 'tools.deny')" />
        <DurationField v-model="draft.startup" label="Start time limit" hint="Empty: omo's default." :examples="['30s', '2m']" :error="fe['timeouts.startup']" />
        <DurationField v-model="draft.call" label="Tool call time limit" hint="Empty: omo's default; at most 24h." :examples="['10m', '1h']" :error="fe['timeouts.call']" />
      </FormSection>

        <p v-if="draft.source" class="mcp-editor__note">Taken over from {{ visibleText(draft.source) }}.</p>
      </template>

      <div v-if="!off" class="mcp-editor__test">
        <UiButton size="sm" :disabled="testing" :aria-busy="testing ? 'true' : undefined" data-mcp-test @click="test">{{ testing ? 'Testing…' : 'Test' }}</UiButton>
        <p class="mcp-editor__note">Starts the form as it is now once, lists its tools and stops it; nothing is saved.</p>
      </div>
      <div v-if="tested" class="mcp-editor__tested" role="status" data-mcp-test-result>
        <p v-if="tested.error" class="mcp-editor__failure">The test failed: {{ visibleText(tested.error) }}</p>
        <template v-else>
          <p>{{ tested.tools.length === 1 ? '1 tool' : `${tested.tools.length} tools` }} in {{ tested.millis }} ms<template v-if="tested.tools.length > 0">: {{ tested.tools.map(visibleText).join(', ') }}</template></p>
          <p v-for="o in tested.omitted" :key="o.tool">Left out: {{ visibleText(o.tool) }} ({{ visibleText(o.reason) }})</p>
          <p v-if="tested.offers_resources || tested.offers_prompts">It also offers resources or prompts, which omo does not relay.</p>
        </template>
      </div>
      <Callout v-if="testError" tone="danger" data-mcp-test-error>
        <p>{{ visibleText(testError) }}</p>
      </Callout>

      <Callout v-if="deleteError" tone="danger" data-delete-error>
        <p>{{ visibleText(deleteError) }}</p>
      </Callout>
      <div v-if="stored">
        <UiButton size="sm" variant="danger" :disabled="deleting" data-mcp-server-delete @click="remove">Delete {{ shownName }}…</UiButton>
      </div>

      <SaveBar
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
    </template>
  </section>
</template>

<style scoped>
.mcp-editor {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-bg);
}
.mcp-editor__head,
.mcp-editor__state,
.mcp-editor__test {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.mcp-editor__test {
  justify-content: flex-start;
}
.mcp-editor__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
  overflow-wrap: anywhere;
}
.mcp-editor__title:focus-visible {
  outline-offset: var(--omo-space);
}
.mcp-editor__new,
.mcp-editor__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.mcp-editor__note {
  flex: 1 1 calc(var(--omo-space) * 50);
  margin: 0;
  overflow-wrap: anywhere;
}
.mcp-editor__state-text,
.mcp-editor__tested {
  display: grid;
  flex: 1 1 calc(var(--omo-space) * 60);
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.mcp-editor__state-text p,
.mcp-editor__tested p {
  margin: 0;
  overflow-wrap: anywhere;
}
.mcp-editor__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.mcp-editor__roles {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 1.5);
}
.mcp-editor__global {
  display: grid;
  gap: var(--omo-space);
}
.mcp-editor__global p {
  margin: 0;
}
.mcp-editor__warn {
  color: var(--omo-warn);
}
.mcp-editor__failure {
  color: var(--omo-danger);
}
.mcp-editor__result {
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
