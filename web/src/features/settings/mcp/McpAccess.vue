<script setup lang="ts">
// Access: how programs that are no omo agent session reach omo's MCP
// endpoint (on another machine, in a container, a desktop app), with the
// commands to connect them, the personal access tokens (the secret is shown
// once, in a dialog, never in a toast or an address) and the OAuth grants
// programs got through omo's consent page; each can be revoked. Such a
// connection gets only the relayed servers marked "Also available without
// an omo session", never board or ticket actions.
import { computed, ref } from 'vue'
import { ApiError } from '../../../api/client'
import { createMCPToken, listMCPTokens, revokeMCPToken } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { MCPToken } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import RelativeTime from '../../../components/ui/RelativeTime.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { fieldOf } from '../../../api/client'
import { askFirst } from '../useLeaveGuard'
import McpCopy from './McpCopy.vue'
import { WITHOUT_SESSION_GETS, connectionCommands } from './mcpForms'
import { EXPIRY_OPTIONS, expiresAt } from './accessForms'

const session = useSession()
const announce = useAnnouncer()

const base = window.location.origin
const endpoint = `${base}/mcp`
const commands = connectionCommands(base)

const tokens = ref<MCPToken[] | null>(null)
const loadError = ref('')
/** omo answered that its token store is not available. */
const unavailable = ref(false)
let seq = 0
async function read() {
  const mine = ++seq
  try {
    const { data } = await listMCPTokens(session.client)
    if (mine !== seq) return
    tokens.value = data ?? []
    loadError.value = ''
    unavailable.value = false
  } catch (e) {
    if (mine !== seq) return
    if (e instanceof ApiError && e.status === 503) unavailable.value = true
    else loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The tokens could not be read.'
  }
}
void read()

const pats = computed(() => (tokens.value ?? []).filter((t) => t.kind === 'pat'))
const grants = computed(() => (tokens.value ?? []).filter((t) => t.kind === 'oauth'))

// Create

const name = ref('')
const expiry = ref('never')
const nameError = ref('')
const createError = ref('')
const creating = ref(false)
/** The new token's secret, shown once; forgotten when the dialog closes. */
const secret = ref('')
const createdName = ref('')

async function create() {
  if (creating.value) return
  nameError.value = ''
  createError.value = ''
  if (name.value.trim() === '') {
    nameError.value = 'Enter a name, e.g. the machine or program the token is for.'
    announce.assertive(nameError.value)
    return
  }
  creating.value = true
  try {
    const exp = expiresAt(expiry.value, new Date())
    const { data } = await createMCPToken(session.client, { name: name.value.trim(), ...(exp === null ? {} : { expires_at: exp }) })
    secret.value = data.secret
    createdName.value = data.token.name
    tokens.value = [...(tokens.value ?? []).filter((t) => t.id !== data.token.id), data.token]
    name.value = ''
    announce.polite(`Token ${visibleText(data.token.name)} created.`)
  } catch (e) {
    if (e instanceof ApiError && e.status === 503) unavailable.value = true
    const msg = failureText(e, { lost: 'No answer from omo; the token may have been created. Its secret is lost then: revoke it in the list.', refused: 'The token was not created.' })
    if (fieldOf(e) === 'name') nameError.value = msg
    else createError.value = msg
    announce.assertive(visibleText(msg))
  } finally {
    creating.value = false
  }
}
function closeSecret() {
  secret.value = ''
  createdName.value = ''
}

// Revoke

const revoking = ref('')
const revokeError = ref<{ id: string; text: string } | null>(null)
async function revoke(t: MCPToken) {
  const what = t.kind === 'pat' ? `the token ${visibleText(t.name)}` : `the access of ${visibleText(t.name)}`
  const ok = await askFirst({
    title: `Revoke ${what}?`,
    text: 'Programs that use it can no longer reach omo. This cannot be undone.',
    confirm: 'Revoke',
    cancel: 'Keep it',
  })
  if (!ok) return
  revoking.value = t.id
  revokeError.value = null
  try {
    await revokeMCPToken(session.client, t.id)
    tokens.value = (tokens.value ?? []).filter((x) => x.id !== t.id)
    announce.polite(`Revoked ${what}.`)
  } catch (e) {
    revokeError.value = { id: t.id, text: failureText(e, { lost: 'No answer from omo; it may be revoked. Look again in a moment.', refused: 'It could not be revoked.' }) }
    announce.assertive(visibleText(revokeError.value.text))
    void read()
  } finally {
    revoking.value = ''
  }
}
</script>

<template>
  <FormSection
    data-mcp-section="access"
    legend="Access"
    description="Programs that are no omo agent session can reach omo's MCP endpoint over the network: an agent program on another machine, in a container, or a desktop app."
  >
    <Callout tone="info" data-mcp-access-scope>
      <p>{{ WITHOUT_SESSION_GETS }}</p>
    </Callout>

    <FormSection legend="Connect a program" :level="3" description="omo's MCP endpoint at the address of this page. Sign in with OAuth (omo asks you on a consent page) or with a personal access token below.">
      <McpCopy label="the MCP endpoint" :text="endpoint" />
      <div v-for="c in commands" :key="c.id" class="access__command" :data-mcp-command="c.id">
        <p class="access__label">{{ c.label }}</p>
        <McpCopy :label="`the commands for ${c.label}`" :text="c.lines.join('\n')" />
      </div>
      <p class="access__note">With a token, put it into the variable OMO_MCP_TOKEN of the program's environment; never into a shared file.</p>
    </FormSection>

    <Callout v-if="unavailable" tone="warn" title="Tokens not available" data-mcp-tokens-unavailable>
      <p>This omo cannot create or list MCP tokens yet. Programs on this computer connect through the entry under Agent programs.</p>
    </Callout>
    <Callout v-if="loadError" tone="danger" live="assertive" data-mcp-tokens-error>
      <p>{{ visibleText(loadError) }}</p>
      <template #actions><UiButton size="sm" @click="read()">Try again</UiButton></template>
    </Callout>

    <template v-if="!unavailable">
      <FormSection legend="Personal access tokens" :level="3" description="A token is shown once, when it is created; omo keeps only a fingerprint of it.">
        <form class="access__create" novalidate data-mcp-token-create @submit.prevent="create">
          <TextField v-model="name" label="Name" hint="What the token is for, e.g. laptop or ci." :error="nameError" autocomplete="off" data-mcp-token-name />
          <SelectField v-model="expiry" label="Valid" :options="EXPIRY_OPTIONS" />
          <div>
            <UiButton type="submit" :disabled="creating" :aria-busy="creating ? 'true' : undefined" data-mcp-token-submit>{{ creating ? 'Creating…' : 'Create token' }}</UiButton>
          </div>
        </form>
        <Callout v-if="createError" tone="danger" data-mcp-token-error>
          <p>{{ visibleText(createError) }}</p>
        </Callout>
        <p v-if="tokens !== null && pats.length === 0" class="access__note" data-no-mcp-tokens>No token yet.</p>
        <ul v-if="pats.length > 0" class="access__list" data-mcp-tokens>
          <li v-for="t in pats" :key="t.id" class="access__row" :data-mcp-token="t.name">
            <div class="access__main">
              <p class="access__name">{{ visibleText(t.name) }}</p>
              <p class="access__meta">
                created <RelativeTime :at="t.created_at" /> ·
                <template v-if="t.last_used_at">last used <RelativeTime :at="t.last_used_at" /></template><template v-else>never used</template> ·
                <template v-if="t.expires_at">ends <RelativeTime :at="t.expires_at" /></template><template v-else>does not end</template>
              </p>
              <p v-if="revokeError?.id === t.id" class="access__error">{{ visibleText(revokeError.text) }}</p>
            </div>
            <UiButton size="sm" variant="danger" :disabled="revoking === t.id" data-mcp-token-revoke @click="revoke(t)">
              Revoke…<span class="sr-only"> {{ visibleText(t.name) }}</span>
            </UiButton>
          </li>
        </ul>
      </FormSection>

      <FormSection legend="OAuth grants" :level="3" description="Programs you approved on omo's consent page. A grant ends when the program does not use it for 30 days.">
        <p v-if="tokens !== null && grants.length === 0" class="access__note" data-no-mcp-grants>No program approved yet.</p>
        <ul v-if="grants.length > 0" class="access__list" data-mcp-grants>
          <li v-for="t in grants" :key="t.id" class="access__row" :data-mcp-grant="t.name">
            <div class="access__main">
              <p class="access__name">{{ visibleText(t.name) }}</p>
              <p class="access__meta">answers went to {{ visibleText(t.redirect_host) }} · client {{ visibleText(t.client_id) }}</p>
              <p class="access__meta">
                approved <RelativeTime :at="t.created_at" /> ·
                <template v-if="t.last_used_at">last used <RelativeTime :at="t.last_used_at" /></template><template v-else>never used</template>
                <template v-if="t.refresh_expires_at"> · ends <RelativeTime :at="t.refresh_expires_at" /> unless used</template>
              </p>
              <p v-if="revokeError?.id === t.id" class="access__error">{{ visibleText(revokeError.text) }}</p>
            </div>
            <UiButton size="sm" variant="danger" :disabled="revoking === t.id" data-mcp-grant-revoke @click="revoke(t)">
              Revoke…<span class="sr-only"> {{ visibleText(t.name) }}</span>
            </UiButton>
          </li>
        </ul>
      </FormSection>
    </template>

    <UiDialog :open="secret !== ''" title="Your new token" eyebrow="MCP access" data-mcp-token-secret @close="closeSecret">
      <div class="access__secret">
        <p>The token {{ visibleText(createdName) }} is shown only now. Copy it into the program's environment as OMO_MCP_TOKEN; omo cannot show it again.</p>
        <McpCopy label="the token" :text="secret" />
      </div>
      <template #actions>
        <UiButton variant="primary" data-primary @click="closeSecret">Done</UiButton>
      </template>
    </UiDialog>
  </FormSection>
</template>

<style scoped>
.access__command,
.access__main,
.access__secret {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
.access__secret {
  gap: calc(var(--omo-space) * 3);
}
.access__label,
.access__note,
.access__secret p {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.access__note {
  color: var(--omo-muted);
}
.access__create {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, calc(var(--omo-space) * 50)), 1fr));
  align-items: end;
  gap: calc(var(--omo-space) * 3);
}
.access__list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.access__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.access__row p {
  margin: 0;
  overflow-wrap: anywhere;
}
.access__name {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.access__meta,
.access__error {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.access__error {
  color: var(--omo-danger);
}
.access__row > :deep(button) {
  flex-shrink: 0;
}
</style>
