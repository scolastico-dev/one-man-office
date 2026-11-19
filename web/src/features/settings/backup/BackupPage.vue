<script setup lang="ts">
// The Backup section of the settings: export the settings to a zip file (on
// request with a copy of the database) and import such a file. An import is
// uploaded and checked first; a dialog shows what it replaces and asks before
// anything changes. Server settings and the database go in only when ticked.
import { computed, ref } from 'vue'
import { ApiError } from '../../../api/client'
import { applyImport, exportSettings, stageImport, stopAllAgents, stopCEO } from '../../../api/endpoints'
import type { ImportPlan, ImportResult } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { agentsRunText, changesAnything, exportFileName, planGroups, resultLines } from './backupPlan'

const def = sectionDef('backup')
const { client } = useSession()
const announce = useAnnouncer()

const messageOf = (e: unknown, fallback: string) => (e instanceof Error && e.message !== '' ? e.message : fallback)

// Export.
const withDatabase = ref(false)
const exporting = ref(false)
const exportError = ref('')

async function runExport() {
  exporting.value = true
  exportError.value = ''
  try {
    const blob = await exportSettings(client, withDatabase.value)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = exportFileName(new Date())
    document.body.append(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 60_000)
    announce.polite('The settings zip is downloaded.')
  } catch (e) {
    exportError.value = messageOf(e, 'The settings could not be exported.')
    announce.assertive(exportError.value)
  } finally {
    exporting.value = false
  }
}

// Import: choose, check, confirm, apply.
const input = ref<HTMLInputElement>()
const file = ref<File | null>(null)
const checking = ref(false)
const importError = ref('')
const plan = ref<ImportPlan | null>(null)
const applyServer = ref(false)
const applyDatabase = ref(false)
const applying = ref(false)
const stopping = ref(false)
const dialogError = ref('')
const result = ref<ImportResult | null>(null)

const groups = computed(() => (plan.value ? planGroups(plan.value) : null))
const blockedByAgents = computed(() => applyDatabase.value && (plan.value?.agents_running ?? 0) > 0)
const canApply = computed(() => plan.value !== null && !applying.value && !stopping.value && !blockedByAgents.value && changesAnything(plan.value, applyServer.value, applyDatabase.value))
const madeOn = computed(() => (plan.value ? new Date(plan.value.manifest.created_at).toLocaleString() : ''))

async function check(f: File) {
  checking.value = true
  importError.value = ''
  try {
    const { data } = await stageImport(client, f)
    plan.value = data
  } catch (e) {
    plan.value = null
    importError.value = messageOf(e, 'The file could not be checked.')
    announce.assertive(importError.value)
  } finally {
    checking.value = false
  }
}

async function chosen(event: Event) {
  const el = event.target as HTMLInputElement
  const f = el.files?.[0] ?? null
  el.value = ''
  if (!f) return
  file.value = f
  result.value = null
  applyServer.value = false
  applyDatabase.value = false
  dialogError.value = ''
  await check(f)
}

function closeDialog() {
  if (applying.value || stopping.value) return
  plan.value = null
}

async function stopAgents() {
  if (!file.value) return
  stopping.value = true
  dialogError.value = ''
  try {
    await stopAllAgents(client)
    await stopCEO(client).catch((e: unknown) => {
      // No engine, or no CEO: nothing to stop.
      if (!(e instanceof ApiError) || e.status >= 500) throw e
    })
    await check(file.value)
  } catch (e) {
    dialogError.value = messageOf(e, 'The agents could not be stopped.')
  } finally {
    stopping.value = false
  }
}

async function apply() {
  const p = plan.value
  if (!p || !canApply.value) return
  applying.value = true
  dialogError.value = ''
  try {
    const { data } = await applyImport(client, { id: p.id, server: applyServer.value && p.server !== '', database: applyDatabase.value && p.database })
    result.value = data
    plan.value = null
    announce.polite(resultLines(data).join(' '))
  } catch (e) {
    dialogError.value = messageOf(e, 'The import failed; nothing was changed.')
    if (e instanceof ApiError && e.code === 'not_found') {
      plan.value = null
      importError.value = dialogError.value
    }
  } finally {
    applying.value = false
  }
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <div class="backup">
      <FormSection
        legend="Export"
        description="A zip file with the general settings, the server settings, projects, agent profiles, roles, prompts, usage scripts, skill sets, plugin settings and the MCP servers omo relays."
        data-backup-export
      >
        <label class="backup__check">
          <input v-model="withDatabase" type="checkbox" data-export-database />
          Include the database (tickets, comments, history)
        </label>
        <Callout tone="warn">
          <p>The file contains your tokens and environment values; keep it private.</p>
        </Callout>
        <div class="backup__row">
          <UiButton variant="primary" :disabled="exporting" data-export @click="runExport">{{ exporting ? 'Exporting…' : 'Export settings' }}</UiButton>
        </div>
        <p v-if="exportError" class="omo-field__error" data-export-error>{{ visibleText(exportError) }}</p>
      </FormSection>

      <FormSection
        legend="Import"
        description="Replaces the settings the zip holds; settings it does not hold stay as they are. You see what changes before anything is written, and omo keeps a copy of every file it replaces."
        data-backup-import
      >
        <input ref="input" type="file" accept=".zip,application/zip" class="sr-only" tabindex="-1" aria-hidden="true" data-import-file @change="chosen" />
        <div class="backup__row">
          <UiButton :disabled="checking" data-import @click="input?.click()">{{ checking ? 'Checking…' : 'Import settings…' }}</UiButton>
        </div>
        <Callout v-if="importError" tone="danger" live="assertive" data-import-error>
          <p>{{ visibleText(importError) }}</p>
        </Callout>
        <Callout v-if="result" tone="info" live="polite" data-import-result>
          <p v-for="line in resultLines(result)" :key="line">{{ visibleText(line) }}</p>
        </Callout>
      </FormSection>
    </div>

    <UiDialog :open="plan !== null" title="Import settings" :eyebrow="file ? visibleText(file.name) : undefined" wide data-import-dialog @close="closeDialog">
      <div v-if="plan && groups" class="backup__plan">
        <p>Made by omo {{ visibleText(plan.manifest.omo_version) }} on {{ madeOn }}.</p>
        <div v-if="groups.replace.length">
          <h3 class="backup__plan-title">Replaced ({{ groups.replace.length }})</h3>
          <ul class="backup__files" data-import-replace>
            <li v-for="p in groups.replace" :key="p"><code>{{ visibleText(p) }}</code></li>
          </ul>
        </div>
        <div v-if="groups.add.length">
          <h3 class="backup__plan-title">Added ({{ groups.add.length }})</h3>
          <ul class="backup__files" data-import-add>
            <li v-for="p in groups.add" :key="p"><code>{{ visibleText(p) }}</code></li>
          </ul>
        </div>
        <p v-if="groups.same" class="backup__muted">{{ groups.same === 1 ? '1 file is' : `${groups.same} files are` }} the same already.</p>
        <p class="backup__muted">Plugins come back as settings; install their files again on the Plugins page where they are missing.</p>

        <label v-if="plan.server !== '' && plan.server !== 'same'" class="backup__check">
          <input v-model="applyServer" type="checkbox" data-import-server />
          Also apply server settings (listen address, sign-in, public address; they apply at the next start)
        </label>

        <template v-if="plan.database">
          <label class="backup__check">
            <input v-model="applyDatabase" type="checkbox" data-import-database />
            Also replace the database
          </label>
          <Callout v-if="applyDatabase" tone="danger" data-import-database-warning>
            <p>
              This replaces all tickets, comments and their history with the ones in the zip. omo pauses now and puts the database in place when it starts
              again, so restart omo afterwards. The current database is kept in a folder below backups.
            </p>
          </Callout>
          <Callout v-if="blockedByAgents" tone="warn" live="polite" data-import-agents>
            <p>{{ agentsRunText(plan.agents_running) }}. The database can be imported only while no agent runs.</p>
            <template #actions>
              <UiButton size="sm" :disabled="stopping" data-import-stop-agents @click="stopAgents">{{ stopping ? 'Stopping…' : 'Stop all agents' }}</UiButton>
            </template>
          </Callout>
        </template>
        <Callout v-if="dialogError" tone="danger" live="assertive" data-import-dialog-error>
          <p>{{ visibleText(dialogError) }}</p>
        </Callout>
      </div>
      <template #actions>
        <UiButton :disabled="applying || stopping" @click="closeDialog">Cancel</UiButton>
        <UiButton :variant="applyDatabase ? 'danger' : 'primary'" :disabled="!canApply" data-import-apply @click="apply">{{ applying ? 'Importing…' : 'Import' }}</UiButton>
      </template>
    </UiDialog>
  </SettingsPage>
</template>

<style scoped>
.backup {
  display: grid;
  gap: calc(var(--omo-space) * 6);
}
.backup__row {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.backup__check {
  display: inline-flex;
  align-items: flex-start;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.backup__check input {
  margin-top: calc(var(--omo-space) * 0.5);
}
.backup__plan {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.backup__plan p {
  margin: 0;
}
.backup__plan-title {
  margin: 0 0 calc(var(--omo-space) * 1);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  font-weight: 600;
}
.backup__files {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  margin: 0;
  padding: 0;
  list-style: none;
  max-height: 40vh;
  overflow: auto;
  /* A composited layer: the compositor then finds this scroll box under a wheel itself, where asking the main thread can drop the wheel. */
  will-change: transform;
}
.backup__files code {
  font-family: inherit;
  overflow-wrap: anywhere;
}
.backup__muted {
  color: var(--omo-muted);
}
</style>
