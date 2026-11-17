<script setup lang="ts">
// Take over: the MCP servers the agent programs' own files have, which omo
// can relay instead, with a pick and a name per server, and the entries
// that stay in the programs and why. Values are masked by omo here (keys
// and ${VAR} references stay visible). A server taken over is created as
// available without an omo session, as it was global in the program; a
// server that signed in with OAuth needs omo's own sign-in afterwards,
// since the program's sign-in belongs to that program. omo stores each
// server before it removes the entry from the programs.
import { computed, reactive, ref } from 'vue'
import { applyMCPTakeover, getMCPTakeover } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { MCPServer, MCPTakeover, MCPTakeoverCandidate, MCPTakeoverReport } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { GLOBAL_LABEL } from './mcpForms'
import { pickErrors, sourceWords, takeoverDetails, targetIdWords } from './takeoverForms'

const props = defineProps<{
  /** The names of omo's stored servers. */
  taken: readonly string[]
}>()
const emit = defineEmits<{ takenOver: [] }>()

const session = useSession()
const announce = useAnnouncer()

const preview = ref<MCPTakeover | null>(null)
const loadError = ref('')
const reading = ref(false)
/** Per candidate key: chosen, and its name in omo. */
const picks = reactive<Record<string, { on: boolean; name: string }>>({})

async function read() {
  reading.value = true
  try {
    const { data } = await getMCPTakeover(session.client)
    preview.value = { candidates: data.candidates ?? [], stays: data.stays ?? [] }
    for (const k of Object.keys(picks)) delete picks[k]
    for (const c of preview.value.candidates) picks[c.key] = { on: c.conflict === '', name: c.name }
    loadError.value = ''
  } catch (e) {
    loadError.value = e instanceof Error && e.message !== '' ? e.message : "The agent programs' servers could not be read."
  } finally {
    reading.value = false
  }
}
void read()

const removeFromPrograms = ref(true)
const chosen = computed(() => (preview.value?.candidates ?? []).filter((c) => picks[c.key]?.on))
const errors = computed(() => pickErrors(chosen.value.map((c) => ({ key: c.key, name: picks[c.key]?.name ?? '' })), props.taken))
const canApply = computed(() => chosen.value.length > 0 && Object.keys(errors.value).length === 0)

const applying = ref(false)
const applyError = ref('')
const report = ref<MCPTakeoverReport | null>(null)

async function apply() {
  if (!canApply.value || applying.value) return
  applying.value = true
  applyError.value = ''
  report.value = null
  try {
    const { data } = await applyMCPTakeover(session.client, {
      take: chosen.value.map((c) => ({ key: c.key, name: (picks[c.key]?.name ?? c.name).trim() })),
      remove_from_cli: removeFromPrograms.value,
    })
    report.value = data
    const stored = data.results.filter((r) => r.stored).length
    announce.polite(`${stored} of ${data.results.length} servers taken over.`)
    emit('takenOver')
    void read()
  } catch (e) {
    applyError.value = failureText(e, { lost: 'No answer from omo; some servers may have been taken over. Look at the servers above.', refused: 'The take-over was refused.' })
    announce.assertive(visibleText(applyError.value))
  } finally {
    applying.value = false
  }
}

const details = (s: MCPServer) => takeoverDetails(s)
const label = (c: MCPTakeoverCandidate) => `Take over ${visibleText(c.name)}`
</script>

<template>
  <FormSection
    data-mcp-section="takeover"
    legend="Take over"
    description="Servers the agent programs have in their own files. omo can relay them instead: then every agent session omo starts gets them, and your own sessions keep them through omo's entry (see Agent programs)."
  >
    <Callout v-if="loadError" tone="danger" live="assertive" data-mcp-takeover-error>
      <p>{{ visibleText(loadError) }}</p>
      <template #actions><UiButton size="sm" @click="read()">Try again</UiButton></template>
    </Callout>
    <p v-if="preview === null && !loadError" class="takeover__note">Reading the agent programs' servers…</p>
    <template v-if="preview">
      <p v-if="preview.candidates.length === 0" class="takeover__note" data-no-takeover>No server to take over.</p>
      <template v-else>
        <p class="takeover__note">
          Values are masked here. Taken-over servers are created with “{{ GLOBAL_LABEL }}” on, as they were available to every session of the program
          before. A server that signed in with OAuth needs login in omo afterwards: the program's own sign-in stays with the program.
        </p>
        <ul class="takeover" data-mcp-takeover>
          <li v-for="c in preview.candidates" :key="c.key" class="takeover__row" :data-mcp-candidate="c.key">
            <div class="takeover__head">
              <Toggle v-if="picks[c.key]" v-model="picks[c.key]!.on" :label="label(c)" hide-label size="sm" :data-mcp-candidate-pick="c.name" />
              <span class="takeover__name">{{ visibleText(c.name) }}</span>
              <StatusBadge v-if="c.needs_login" tone="warn" text="needs login after" />
              <StatusBadge tone="accent" text="also without session" :title="GLOBAL_LABEL" />
            </div>
            <p class="takeover__meta">{{ visibleText(sourceWords(c.sources)) }}</p>
            <p v-for="(d, i) in details(c.server)" :key="i" class="takeover__detail">{{ visibleText(d) }}</p>
            <p v-for="(n, i) in c.notes" :key="`n${i}`" class="takeover__meta">{{ visibleText(n) }}</p>
            <p v-if="c.conflict" class="takeover__warn" data-mcp-candidate-conflict>{{ visibleText(c.conflict) }}</p>
            <TextField
              v-if="picks[c.key]?.on"
              v-model="picks[c.key]!.name"
              :label="`Name in omo for ${visibleText(c.name)}`"
              :error="errors[c.key]"
              spellcheck="false"
              autocomplete="off"
              autocapitalize="off"
              :data-mcp-candidate-name="c.key"
            />
          </li>
        </ul>
        <Toggle v-model="removeFromPrograms" label="Remove the taken-over entries from the programs' files" data-mcp-takeover-remove />
        <p class="takeover__note">Off, the programs keep their entries too and show the tools twice. omo keeps a copy of each file before it changes it.</p>
        <div class="takeover__buttons">
          <UiButton variant="primary" :disabled="!canApply || applying" :aria-busy="applying ? 'true' : undefined" data-mcp-takeover-apply @click="apply">
            {{ applying ? 'Taking over…' : chosen.length === 1 ? 'Take over 1 server' : `Take over ${chosen.length} servers` }}
          </UiButton>
          <UiButton size="sm" variant="ghost" :disabled="reading" @click="read()">Read the files again</UiButton>
        </div>
      </template>
      <Callout v-if="applyError" tone="danger" data-mcp-takeover-apply-error>
        <p>{{ visibleText(applyError) }}</p>
      </Callout>
      <div v-if="report" class="takeover__report" role="status" data-mcp-takeover-report>
        <p v-for="r in report.results" :key="r.name" :class="{ 'takeover__warn': !r.stored }">
          <template v-if="r.stored">
            {{ visibleText(r.name) }}: stored in omo<template v-if="r.needs_login">; needs login: open it under Servers and Connect</template>.
            <template v-for="rm in r.removed" :key="rm.target">
              {{ rm.done ? `Removed from ${visibleText(targetIdWords(rm.target))}.` : `Still in ${visibleText(targetIdWords(rm.target))}: ${visibleText(rm.error)}; remove it by hand or try again.` }}
            </template>
          </template>
          <template v-else>{{ visibleText(r.name) }}: not taken over: {{ visibleText(r.error) }}</template>
        </p>
        <p v-if="report.backups.length > 0" class="takeover__meta">Copies of the files before: {{ report.backups.map(visibleText).join(', ') }}</p>
      </div>
      <template v-if="preview.stays.length > 0">
        <p class="takeover__sub">Entries that stay in the programs</p>
        <ul class="takeover takeover--stays" data-mcp-stays>
          <li v-for="s in preview.stays" :key="`${s.target}/${s.entry_name}`" class="takeover__row">
            <p class="takeover__name">{{ visibleText(s.entry_name) }} <span class="takeover__meta">in {{ visibleText(targetIdWords(s.target)) }}</span></p>
            <p class="takeover__meta">{{ visibleText(s.reason) }}</p>
          </li>
        </ul>
      </template>
    </template>
  </FormSection>
</template>

<style scoped>
.takeover {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.takeover__row {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.takeover p,
.takeover__report p {
  margin: 0;
  overflow-wrap: anywhere;
}
.takeover__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.takeover__name {
  overflow-wrap: anywhere;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.takeover__meta,
.takeover__detail,
.takeover__note,
.takeover__warn {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.takeover__detail {
  color: var(--omo-fg);
}
.takeover__note,
.takeover__sub {
  margin: 0;
}
.takeover__sub {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.takeover__warn {
  color: var(--omo-warn);
}
.takeover__buttons {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.takeover__report {
  display: grid;
  gap: var(--omo-space);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
