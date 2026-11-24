<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import type { LSPServer } from '../../../api/types'
import EnvTable from '../../../components/ui/EnvTable.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import LspJsonField from './LspJsonField.vue'

const props = defineProps<{ index: number; errors: Readonly<Record<string, string>> }>()
const model = defineModel<LSPServer>({ required: true })
defineEmits<{ remove: [] }>()
const root = ref<HTMLElement>()
const prefix = computed(() => `lsp.servers.${props.index}`)
const field = (key: string) => `${prefix.value}.${key}`
const err = (key: string) => props.errors[field(key)]
const argsErrors = computed(() => Object.fromEntries(model.value.args.map((_, i) => [i, err(`args.${i}`)]).filter((p) => p[1])))
const envErrors = computed(() => Object.fromEntries(Object.keys(model.value.env).map((name) => [name, err(`env.${name}`)]).filter((p) => p[1])))

async function addSelector() {
  model.value.files.push({ pattern: '', language_id: '' })
  await nextTick()
  root.value?.querySelector<HTMLInputElement>(`[data-selector="${model.value.files.length - 1}"] input`)?.focus()
}
function moveSelector(index: number, offset: number) {
  const files = [...model.value.files]
  const to = index + offset
  ;[files[index], files[to]] = [files[to]!, files[index]!]
  model.value.files = files
}
</script>

<template>
  <div ref="root" class="lsp-server" :data-lsp-server="index">
    <FormSection :legend="`Language server ${index + 1}`" :level="3">
      <TextField v-model="model.name" label="Registration name" hint="Unique name: lowercase letters, digits, dots, underscores or hyphens; at most 64 characters." :error="err('name')" :data-field="field('name')" />
      <Toggle v-model="model.enabled" label="Enabled" :data-field="field('enabled')" />
      <TextField v-model="model.command" label="Command" hint="An executable name or path. A bare name uses PATH; this is not a shell command." :error="err('command')" :data-field="field('command')" />
      <ListField v-model="model.args" label="Arguments" item-label="Argument" add-label="Add argument" empty-text="No arguments." hint="Each row is one literal argument; no shell expansion." :error="err('args')" :errors="argsErrors" data-lsp-args />
      <EnvTable v-model="model.env" label="Environment overrides" hint="Literal values; no variable or backtick expansion." empty-text="No overrides." :error="err('env')" :errors="envErrors" data-lsp-env />
      <FormSection legend="File selection" :level="4" description="Selectors are tried in order; the first match supplies the language ID. Without a slash, a pattern matches the basename at any depth. With a slash, it matches the worktree-relative path. Case-sensitive; use *, ? or character classes, not ** or backslashes.">
        <p v-if="err('files')" class="omo-field__error">{{ err('files') }}</p>
        <div v-for="(selector, i) in model.files" :key="i" class="lsp-server__selector" :data-selector="i">
          <TextField v-model="selector.pattern" :label="`File pattern ${i + 1}`" placeholder="*.go" :error="err(`files.${i}.pattern`)" :data-field="field(`files.${i}.pattern`)" />
          <TextField v-model="selector.language_id" :label="`Language ID ${i + 1}`" placeholder="go" :error="err(`files.${i}.language_id`)" :data-field="field(`files.${i}.language_id`)" />
          <div class="lsp-server__actions">
            <UiButton size="sm" :disabled="i === 0" :aria-label="`Move file selector ${i + 1} up`" data-move-up @click="moveSelector(i, -1)">Move up</UiButton>
            <UiButton size="sm" :disabled="i === model.files.length - 1" :aria-label="`Move file selector ${i + 1} down`" data-move-down @click="moveSelector(i, 1)">Move down</UiButton>
            <UiButton size="sm" :aria-label="`Remove file selector ${i + 1}`" data-remove-selector @click="model.files.splice(i, 1)">Remove selector</UiButton>
          </div>
        </div>
        <div><UiButton size="sm" @click="addSelector">Add file selector</UiButton></div>
      </FormSection>
      <LspJsonField v-model="model.initialization_options" label="Initialization options (JSON)" hint="A JSON object sent as initializationOptions when the server starts; at most 64 KiB." :field="field('initialization_options')" :error="err('initialization_options')" />
      <LspJsonField v-model="model.configuration" label="Server configuration (JSON)" hint="A JSON object of server settings for configuration requests and changes; at most 64 KiB." :field="field('configuration')" :error="err('configuration')" />
      <div><UiButton variant="danger" size="sm" data-remove-server @click="$emit('remove')">Remove language server</UiButton></div>
    </FormSection>
  </div>
</template>

<style scoped>
.lsp-server {
  min-width: 0;
  padding: calc(var(--omo-space) * 4);
  border: var(--omo-border) solid var(--omo-line);
}
.lsp-server__selector {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding-bottom: calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.lsp-server__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
</style>
