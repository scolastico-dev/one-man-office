<script setup lang="ts">
// Runs the text of a script, saved or not, once and now, as the account of a
// profile: with that profile's real credentials, through omo's real
// helpers. Nothing is stored. The answer is shown as text only: the windows
// as meters with the limits that apply to them, the error the script ended
// with, its log and how long it took. Everything in it comes from the
// script, so names, errors and log lines go through visibleText.
import { computed, ref, watch } from 'vue'
import { fieldOf } from '../../../api/client'
import { getProfiles, testUsageScript } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { UsageSettings, UsageTestResult } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import SelectField from '../../../components/ui/SelectField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UsageMeter from '../../../components/ui/UsageMeter.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleLines, visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { testProfiles, thresholdsOf } from './usageForms'

const props = withDefaults(
  defineProps<{
    name: string
    /** The text to run: the editor's draft. */
    draft: string
    /** The profiles that use this script, offered first. */
    usedBy?: readonly string[]
    /** The stored limits, for the marks on the meters. */
    usage?: UsageSettings | null
  }>(),
  { usedBy: () => [], usage: null },
)

const { client } = useSession()
const announce = useAnnouncer()

const profiles = ref<string[]>([])
const options = computed(() => testProfiles(profiles.value, props.usedBy))
const profile = ref('')
watch(
  options,
  (now) => {
    if (!now.some((o) => o.value === profile.value)) profile.value = now[0]?.value ?? ''
  },
  { immediate: true },
)
void (async () => {
  try {
    const { data } = await getProfiles(client)
    profiles.value = data.profiles.map((p) => p.name)
  } catch {
    // Without the list a test still runs without a profile.
  }
})()

const running = ref(false)
const result = ref<(UsageTestResult & { profile: string }) | null>(null)
const refusal = ref('')
const profileError = ref('')

async function run() {
  if (running.value) return
  running.value = true
  refusal.value = ''
  profileError.value = ''
  const as = profile.value
  try {
    const { data } = await testUsageScript(client, props.name, { text: props.draft, profile: as })
    result.value = { ...data, profile: as }
    announce.polite(data.error ? 'The script failed.' : `The script reported ${data.windows.length} ${data.windows.length === 1 ? 'window' : 'windows'}.`)
  } catch (e) {
    const text = failureText(e, { lost: 'No answer from omo; the test may still be running. Try again in a minute.', refused: 'The test was refused.' })
    if (fieldOf(e) === 'profile') profileError.value = text
    else refusal.value = text
    announce.assertive(visibleText(text))
  } finally {
    running.value = false
  }
}

const ranAs = computed(() => (result.value === null ? '' : result.value.profile === '' ? 'without a profile' : `as ${visibleText(result.value.profile)}`))
</script>

<template>
  <FormSection
    legend="Test"
    :level="4"
    description="Runs the text above now, saved or not, as the chosen profile: with its real credentials, as you. Nothing is stored."
  >
    <div class="script-test" data-script-test>
      <div class="script-test__run">
        <SelectField v-model="profile" label="Run as" :options="options" :error="profileError || undefined" data-test-profile />
        <UiButton class="script-test__button" variant="primary" :disabled="running" :aria-busy="running ? 'true' : undefined" data-test-run @click="run">
          {{ running ? 'Testing…' : 'Test' }}
        </UiButton>
      </div>
      <Callout v-if="refusal" tone="danger" live="assertive" data-test-refused>
        <p>{{ visibleText(refusal) }}</p>
      </Callout>
      <div v-if="result" class="script-test__result" role="status" aria-label="Test result" data-test-result>
        <p class="script-test__head">Ran {{ ranAs }} · took {{ result.millis }} ms</p>
        <Callout v-if="result.error" tone="danger" title="The script failed" data-test-error>
          <p class="script-test__error">{{ visibleText(result.error) }}</p>
        </Callout>
        <ul v-if="result.windows.length" class="script-test__meters" aria-label="Windows" data-test-windows>
          <li v-for="(w, i) in result.windows" :key="i" :data-test-window="w.name">
            <UsageMeter
              :label="w.name"
              :percent="w.used_percent"
              :soft="thresholdsOf(w.name, usage).soft"
              :hard="thresholdsOf(w.name, usage).hard"
              :resets-at="w.resets_at"
            />
          </li>
        </ul>
        <p v-else-if="!result.error" class="script-test__head" data-test-no-windows>The script reported no window.</p>
        <div v-if="result.log.length" class="script-test__log">
          <p class="script-test__log-label">Log</p>
          <pre class="script-test__log-text" tabindex="0" aria-label="Log of the test" data-test-log>{{ visibleLines(result.log.join('\n')) }}</pre>
        </div>
      </div>
    </div>
  </FormSection>
</template>

<style scoped>
.script-test {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.script-test__run {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the select below its one-line label. */
.script-test__button {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
  min-height: calc(var(--omo-space) * 10);
}
.script-test__result {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.script-test__head {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.script-test__error {
  overflow-wrap: anywhere;
}
.script-test__meters {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.script-test__log {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.script-test__log-label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.script-test__log-text {
  max-height: calc(var(--omo-space) * 50);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: pre;
}
.script-test__log-text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
</style>
