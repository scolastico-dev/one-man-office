<script setup lang="ts">
// Agent programs: per program and configuration file (the program's own
// file and the file of every account folder of the profiles) whether omo is
// registered there. Apply and Remove first show the plan (the file, the
// exact commands and omo's entry before and after) and run it only when it
// is confirmed; replacing an entry named omo that is not omo's needs a
// second click. omo changes the files only through the programs' own
// commands and keeps a copy of each file first. Values of the files are
// masked by omo; the commands are shown as they run.
import { computed, ref } from 'vue'
import { ApiError } from '../../../api/client'
import { applyMCPClient, listMCPClients, planMCPClient } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { MCPClientApplied, MCPClientPlan, MCPClientState } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleLines, visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { actionLabel, clientActions, clientWords, cliLabel, commandLine, entryText, targetWords } from './mcpForms'

const session = useSession()
const announce = useAnnouncer()

const states = ref<MCPClientState[] | null>(null)
const loadError = ref('')
let seq = 0
async function read() {
  const mine = ++seq
  try {
    const { data } = await listMCPClients(session.client)
    if (mine !== seq) return
    states.value = data ?? []
    loadError.value = ''
  } catch (e) {
    if (mine !== seq) return
    loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The agent programs could not be read.'
  }
}
void read()

function take(st: MCPClientState) {
  const list = states.value ?? []
  states.value = list.map((x) => (x.target.id === st.target.id ? st : x))
}

// The plan

const plan = ref<MCPClientPlan | null>(null)
const planOf = ref<MCPClientState | null>(null)
const planning = ref('')
const planError = ref('')
const applying = ref(false)
const applyError = ref('')
const confirming = ref(false)
const done = ref<{ target: string; text: string } | null>(null)

/** The plan replaces an entry named omo that is not omo's: it needs a second click. */
const replacesForeign = computed(() => plan.value?.action === 'install' && planOf.value?.state === 'foreign' && (plan.value?.commands.length ?? 0) > 0)

async function showPlan(st: MCPClientState, action: 'install' | 'remove') {
  planning.value = `${st.target.id}:${action}`
  planError.value = ''
  done.value = null
  try {
    const { data } = await planMCPClient(session.client, st.target.id, action)
    plan.value = data
    planOf.value = st
    applyError.value = ''
    confirming.value = false
  } catch (e) {
    planError.value = failureText(e, { lost: 'No answer from omo; try again.', refused: 'omo could not plan this change.' })
    announce.assertive(visibleText(planError.value))
  } finally {
    planning.value = ''
  }
}

function closePlan() {
  if (applying.value) return
  plan.value = null
  planOf.value = null
}

function appliedOf(e: unknown): MCPClientApplied | null {
  if (!(e instanceof ApiError)) return null
  const d = e.details as Partial<MCPClientApplied> | undefined
  return d && typeof d === 'object' && d.state && typeof d.state === 'object' ? (d as MCPClientApplied) : null
}

async function apply() {
  const p = plan.value
  if (!p || applying.value) return
  if (replacesForeign.value && !confirming.value) {
    confirming.value = true
    announce.assertive('This replaces an entry named omo that is not omo’s. Click again to replace it.')
    return
  }
  applying.value = true
  applyError.value = ''
  try {
    const { data } = await applyMCPClient(session.client, p.target.id, p.hash)
    take(data.state)
    const what = p.action === 'install' ? `omo is registered in ${targetWords(p.target)}` : `omo is removed from ${targetWords(p.target)}`
    done.value = { target: p.target.id, text: data.backup ? `${what}. A copy of the file before is at ${data.backup}.` : `${what}.` }
    announce.polite(visibleText(done.value.text))
    plan.value = null
    planOf.value = null
  } catch (e) {
    const after = appliedOf(e)
    if (after) take(after.state)
    applyError.value = failureText(e, { lost: 'No answer from omo; the commands may have run. Close this and look at the state.', refused: 'The change was refused.' })
    announce.assertive(visibleText(applyError.value))
  } finally {
    applying.value = false
  }
}

const title = computed(() => {
  const p = plan.value
  if (!p) return ''
  return p.action === 'install' ? `Register omo in ${cliLabel(p.target.cli)}` : `Remove omo from ${cliLabel(p.target.cli)}`
})
const applyLabel = computed(() => {
  if (applying.value) return 'Running…'
  if (confirming.value) return 'Replace the other entry'
  return plan.value?.action === 'remove' ? 'Remove' : 'Apply'
})
</script>

<template>
  <FormSection
    data-mcp-section="programs"
    legend="Agent programs"
    description="Registers omo in the agent programs, so that your own sessions of them (started outside omo) reach omo's relayed servers too. omo changes the files only through each program's own commands and keeps a copy of the file first."
  >
    <Callout v-if="loadError" tone="danger" live="assertive" data-mcp-clients-error>
      <p>{{ visibleText(loadError) }}</p>
      <template #actions><UiButton size="sm" @click="read()">Try again</UiButton></template>
    </Callout>
    <p v-if="states === null && !loadError" class="clients__note">Reading the agent programs' files…</p>
    <p v-else-if="states !== null && states.length === 0" class="clients__note" data-no-mcp-clients>
      No agent program found: omo looks at the files of Claude Code, Codex and Antigravity, and at the account folders of your profiles.
    </p>
    <ul v-else-if="states !== null" class="clients" data-mcp-clients>
      <li v-for="st in states" :key="st.target.id" class="clients__row" :data-mcp-client="st.target.id">
        <div class="clients__main">
          <p class="clients__name">
            <span>{{ visibleText(targetWords(st.target)) }}</span>
            <StatusBadge :tone="clientWords(st).tone" :text="clientWords(st).text" data-mcp-client-state />
          </p>
          <p class="clients__meta">{{ visibleText(st.target.config_path) }}</p>
          <p v-if="st.error" class="clients__error">{{ visibleText(st.error) }}</p>
          <p v-if="st.state === 'foreign'" class="clients__warn">The file has an entry named omo that is not omo's: whoever uses it may not be who omo thinks.</p>
          <p v-if="done?.target === st.target.id" class="clients__done" data-mcp-client-done>{{ visibleText(done.text) }}</p>
        </div>
        <div class="clients__buttons">
          <UiButton
            v-for="a in clientActions(st)"
            :key="a"
            size="sm"
            :variant="a === 'install' ? 'primary' : 'default'"
            :disabled="planning !== ''"
            :aria-busy="planning === `${st.target.id}:${a}` ? 'true' : undefined"
            :data-mcp-client-action="a"
            @click="showPlan(st, a)"
          >
            {{ actionLabel(st, a) }}<span class="sr-only"> {{ visibleText(targetWords(st.target)) }}</span>
          </UiButton>
        </div>
      </li>
    </ul>
    <Callout v-if="planError" tone="danger" data-mcp-plan-error>
      <p>{{ visibleText(planError) }}</p>
    </Callout>

    <UiDialog :open="plan !== null" :title="title" eyebrow="agent programs" wide data-mcp-plan @close="closePlan">
      <template v-if="plan">
        <div class="plan">
          <p>File: <code>{{ visibleText(plan.file) }}</code></p>
          <Callout v-if="replacesForeign" :tone="confirming ? 'danger' : 'warn'" data-mcp-plan-foreign>
            <p>{{ visibleText(plan.message) }}</p>
            <p v-if="confirming">Click “Replace the other entry” once more to replace it.</p>
          </Callout>
          <p v-else-if="plan.message" data-mcp-plan-message>{{ visibleText(plan.message) }}</p>
          <template v-if="plan.commands.length > 0">
            <p>omo runs:</p>
            <pre class="plan__code" tabindex="0" aria-label="Commands omo runs" data-mcp-plan-commands>{{ visibleLines(plan.commands.map(commandLine).join('\n')) }}</pre>
            <div class="plan__entries">
              <div>
                <p>omo's entry now</p>
                <pre class="plan__code" tabindex="0" aria-label="omo's entry now">{{ visibleLines(entryText(plan.before)) }}</pre>
              </div>
              <div>
                <p>omo's entry after</p>
                <pre class="plan__code" tabindex="0" aria-label="omo's entry after" data-mcp-plan-after>{{ visibleLines(entryText(plan.after)) }}</pre>
              </div>
            </div>
            <p class="plan__note">The other entries of the file stay as they are.</p>
          </template>
          <Callout v-if="applyError" tone="danger" live="assertive" data-mcp-apply-error>
            <p>{{ visibleText(applyError) }}</p>
          </Callout>
        </div>
      </template>
      <template #actions>
        <UiButton :disabled="applying" @click="closePlan">{{ plan && plan.commands.length > 0 ? 'Cancel' : 'Close' }}</UiButton>
        <UiButton
          v-if="plan && plan.commands.length > 0"
          :variant="confirming || plan.action === 'remove' ? 'danger' : 'primary'"
          :disabled="applying"
          :aria-busy="applying ? 'true' : undefined"
          data-primary
          data-mcp-plan-apply
          @click="apply"
        >
          {{ applyLabel }}
        </UiButton>
      </template>
    </UiDialog>
  </FormSection>
</template>

<style scoped>
.clients {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.clients__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.clients__main {
  display: grid;
  flex: 1 1 calc(var(--omo-space) * 60);
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.clients__row p,
.plan p {
  margin: 0;
  overflow-wrap: anywhere;
}
.clients__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.clients__meta,
.clients__note,
.clients__warn,
.clients__error,
.clients__done,
.plan__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.clients__note {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.clients__warn {
  color: var(--omo-warn);
}
.clients__error {
  color: var(--omo-danger);
}
.clients__done {
  color: var(--omo-ok);
}
.clients__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.plan {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.plan__entries {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, calc(var(--omo-space) * 60)), 1fr));
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.plan__entries > div {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
.plan__code {
  max-height: calc(var(--omo-space) * 60);
  margin: 0;
  padding: calc(var(--omo-space) * 2);
  overflow: auto;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: pre;
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
