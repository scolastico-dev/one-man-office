<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import type { CLI, LSPServer } from '../../../api/types'
import DurationField from '../../../components/ui/DurationField.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { CLI_OPTIONS } from '../agents/profileForm'
import { useGeneralForm } from '../general/generalForms'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import LspServerEditor from './LspServerEditor.vue'

const def = sectionDef('lsp')
const form = useGeneralForm(['lsp'])
const draft = form.draft
const root = ref<HTMLElement>()
const forceChatErrorId = useId()
const errors = form.fieldErrors
const forceChatErrors = computed(() => Object.entries(errors.value).filter(([key]) => key === 'lsp.force_chat' || key.startsWith('lsp.force_chat.')).map(([, message]) => message))

const placed = computed(() => {
  const keys = ['lsp.debounce', ...Object.keys(errors.value).filter((key) => key === 'lsp.force_chat' || key.startsWith('lsp.force_chat.'))]
  draft.value?.lsp.servers.forEach((s, i) => {
    const prefix = `lsp.servers.${i}`
    for (const key of ['name', 'command', 'args', 'env', 'files', 'initialization_options', 'configuration']) keys.push(`${prefix}.${key}`)
    s.args.forEach((_, j) => keys.push(`${prefix}.args.${j}`))
    Object.keys(s.env).forEach((name) => keys.push(`${prefix}.env.${name}`))
    s.files.forEach((_, j) => keys.push(`${prefix}.files.${j}.pattern`, `${prefix}.files.${j}.language_id`))
  })
  return keys
})
const unplaced = computed(() => unplacedErrors(errors.value, placed.value))

// Keep editors (and invalid JSON text) attached to their registration on removal.
const keys = new WeakMap<LSPServer, number>()
let nextKey = 0
function serverKey(server: LSPServer): number {
  if (!keys.has(server)) keys.set(server, nextKey++)
  return keys.get(server)!
}
async function addServer() {
  if (!draft.value) return
  draft.value.lsp.servers.push({ name: '', enabled: false, command: '', args: [], env: {}, files: [{ pattern: '', language_id: '' }], initialization_options: {}, configuration: {} })
  await nextTick()
  root.value?.querySelector<HTMLInputElement>(`[data-lsp-server="${draft.value.lsp.servers.length - 1}"] input`)?.focus()
}
function forceChat(cli: CLI, on: boolean) {
  if (!draft.value) return
  const current = draft.value.lsp.force_chat
  draft.value.lsp.force_chat = on ? [...current.filter((value) => value !== cli), cli] : current.filter((value) => value !== cli)
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" :resource="form">
    <div v-if="draft" ref="root" class="lsp">
      <UnplacedErrors :errors="unplaced" />
      <FormSection legend="Language servers" description="Registered language servers run in managed worktrees. Their working directory is always the worktree root. Install the executables yourself before enabling them; omo does not install servers.">
        <p v-if="!draft.lsp.servers.length" class="lsp__note">No language servers registered.</p>
        <LspServerEditor v-for="(server, i) in draft.lsp.servers" :key="serverKey(server)" v-model="draft.lsp.servers[i]!" :index="i" :errors="errors" @remove="draft.lsp.servers.splice(i, 1)" />
        <div><UiButton @click="addServer">Add language server</UiButton></div>
      </FormSection>
      <FormSection legend="Reporting issues">
        <DurationField v-model="draft.lsp.debounce" label="Debounce" hint="Quiet interval before reporting changed diagnostics. 0s disables the delay; maximum 1m." :examples="['0s', '500ms', '1s']" :error="errors['lsp.debounce']" data-field="lsp.debounce" />
        <FormSection legend="Force chat by agent program" :level="3" description="Use the fallback for these agent programs: send a chat message even when the agent is listening for MCP notifications. Leave a program off to use MCP when its agent is listening, with chat as the fallback otherwise.">
          <div role="group" aria-label="Agent programs forced to chat" :aria-invalid="forceChatErrors.length ? 'true' : undefined" :aria-describedby="forceChatErrors.length ? forceChatErrorId : undefined" class="lsp__programs">
            <Toggle v-for="cli in CLI_OPTIONS" :key="cli.value" :label="cli.label" :model-value="draft.lsp.force_chat.includes(cli.value)" :data-force-chat="cli.value" @update:model-value="forceChat(cli.value, $event)" />
          </div>
          <div v-if="forceChatErrors.length" :id="forceChatErrorId"><p v-for="message in forceChatErrors" :key="message" class="omo-field__error">{{ message }}</p></div>
        </FormSection>
      </FormSection>
    </div>
  </SettingsPage>
</template>

<style scoped>
.lsp {
  display: grid;
  gap: calc(var(--omo-space) * 6);
  min-width: 0;
}
.lsp__programs {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
}
.lsp__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
}
</style>
