<script setup lang="ts">
// The Environment section of the settings: the variables every agent starts
// with (the `agent_env` key of settings.yaml). Secret values are write-only:
// the server sends a placeholder, and sending it back keeps the stored value.
import { computed, ref, watch } from 'vue'
import { ApiError, fieldOf } from '../../../api/client'
import { checkAgentEnv, getSettings, patchSettings } from '../../../api/endpoints'
import type { ApiEvent, SettingsChangedData } from '../../../api/types'
import { HIDDEN_VALUE } from '../../../api/types'
import EnvTable from '../../../components/ui/EnvTable.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { useSession } from '../../../session'
import SettingsPage from '../SettingsPage.vue'
import UnplacedErrors from '../UnplacedErrors.vue'
import { sectionDef } from '../settingsSections'
import { askFirst } from '../useLeaveGuard'
import { sameJSON, useSettingsResource } from '../useSettingsResource'
import EnvCheck from './EnvCheck.vue'
import { checkedOf, currentChecks, formErrors, formOf, KEY, rowErrors, SHIPPED_ENV, shippedCopy, type Checked, type EnvForm } from './envForm'

const def = sectionDef('environment')
const { client } = useSession()
const say = useAnnouncer()

const watches = (ev: ApiEvent) =>
  ev.kind === 'settings.invalid' || (ev.kind === 'settings.changed' && (ev.data as SettingsChangedData).section === 'general')

const resource = useSettingsResource<EnvForm>({
  async load() {
    const { data } = await getSettings(client)
    return { value: formOf(data.settings), revision: data.revision, errors: data.errors }
  },
  async save(draft, revision) {
    const { data, eventId } = await patchSettings(client, { [KEY]: draft.agent_env }, revision)
    return { value: formOf(data.settings), revision: data.revision, eventId }
  },
  watches,
  keys: [KEY],
})

const env = computed({
  get: () => resource.draft.value?.agent_env ?? {},
  set(v: Record<string, string>) {
    if (resource.draft.value) resource.draft.value.agent_env = v
  },
})

// The check. Each request takes a number; only the newest one's answer counts.
const results = ref<Checked[]>([])
const refusal = ref<{ name: string; value: string; message: string } | null>(null)
const checkError = ref('')
const checking = ref(false)
let asked = 0

const checks = computed(() => currentChecks(results.value, env.value))
const rowNotes = computed(() => {
  const out: Record<string, string> = {}
  for (const c of checks.value) if (c.state === 'failed' && c.message) out[c.name] = c.message
  const r = refusal.value
  if (r !== null && env.value[r.name] === r.value) out[r.name] = r.message
  Object.assign(out, rowErrors(resource.fieldErrors.value))
  return out
})
/** A refusal of the variables as a whole goes under the table; one naming another key above the form. */
const tableError = computed(() => resource.fieldErrors.value[KEY])
const formNotes = computed(() => formErrors(resource.fieldErrors.value).filter((m) => m !== tableError.value))

// Results describe the values they were computed for. Once a save goes
// through or a read brings other stored values, agents get something else:
// the results (and a refusal of the check) go, and a new check is needed.
watch(
  () => [resource.revision.value, resource.value.value] as const,
  ([rev, stored], [revBefore, storedBefore]) => {
    if (revBefore === null || storedBefore === null) return
    if (rev === revBefore && sameJSON(stored, storedBefore)) return
    asked++
    checking.value = false
    results.value = []
    refusal.value = null
  },
)

// A refusal of the check stays under its row only while the row is as it was.
watch(env, (now) => {
  const r = refusal.value
  if (r !== null && now[r.name] !== r.value) refusal.value = null
})

async function check() {
  if (checking.value) return
  const sent = { ...env.value }
  const n = ++asked
  checking.value = true
  checkError.value = ''
  refusal.value = null
  try {
    const { data } = await checkAgentEnv(client, sent)
    if (n !== asked) return
    results.value = checkedOf(data, sent)
    say.polite('The environment check finished.')
  } catch (e) {
    if (n !== asked) return
    results.value = []
    const message = e instanceof ApiError ? e.message : 'The check could not be run.'
    const field = fieldOf(e)
    const name = field !== null && field.startsWith(`${KEY}.`) ? field.slice(KEY.length + 1) : null
    if (name !== null && Object.hasOwn(sent, name)) refusal.value = { name, value: sent[name]!, message }
    else checkError.value = message
    say.assertive(message)
  } finally {
    if (n === asked) checking.value = false
  }
}

async function restore() {
  const ok = await askFirst({
    title: 'Restore the shipped defaults?',
    text: 'The variables below are replaced by the ones omo ships. Nothing is saved until you press Save.',
    confirm: 'Restore',
    cancel: 'Keep my variables',
  })
  if (!ok || resource.draft.value === null) return
  resource.draft.value.agent_env = shippedCopy()
  say.polite('The shipped defaults are in the form. Save to keep them.')
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" :resource="resource">
    <FormSection legend="Variables">
      <template #description>
        <p>
          Every agent starts with these. <code>$VAR</code> and <code>${VAR:-fallback}</code> use omo's own environment; <code>`command`</code> runs when an agent starts
          and its output becomes the value.
        </p>
        <p>
          A key written out here is stored as typed in settings.yaml. A command such as <code>`pass show anthropic`</code> keeps it in your password manager. A saved secret
          is not shown again; you can replace or remove it.
        </p>
        <p>Profile and model environments override these. omo's own git client uses them for merge commits. They apply to agents started after the save.</p>
      </template>
      <UnplacedErrors :errors="formNotes" />
      <EnvTable
        v-model="env"
        label="Agent environment"
        :hidden="HIDDEN_VALUE"
        :shipped="SHIPPED_ENV"
        :errors="rowNotes"
        :error="tableError"
        empty-text="No variables."
      />
      <div class="env-page__buttons">
        <UiButton size="sm" variant="ghost" data-restore-defaults @click="restore">Restore shipped defaults…</UiButton>
      </div>
      <EnvCheck :checks="checks" :busy="checking" :error="checkError" @check="check" />
    </FormSection>
  </SettingsPage>
</template>

<style scoped>
.env-page__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 3);
}
</style>
