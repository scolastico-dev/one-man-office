<script setup lang="ts">
// The limits of the Usage page: where agents start to wait (soft), where
// running agents are stopped (hard), how often the scripts run, and limits of
// their own for single windows. It edits the `usage` key of settings.yaml
// through the page's form and saves it with its own Save bar; the windows are
// sent whole, as the server replaces them whole. Text that is no number
// blocks Save until it is one; the server checks the bounds and names the
// field it refuses.
import { computed, ref, useId, watch } from 'vue'
import type { UsageThreshold } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import DurationField from '../../../components/ui/DurationField.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import IconButton from '../../../components/ui/IconButton.vue'
import NumberField from '../../../components/ui/NumberField.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import FileErrors from '../FileErrors.vue'
import type { GeneralForm } from '../general/generalForms'
import { sameJSON, diskChangeText, focusFirstInvalid } from '../useSettingsResource'
import { useLeaveGuard } from '../useLeaveGuard'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import { overrideError, placedLimitField, rowsOf, windowsOf, type OverrideRow } from './usageForms'

const props = defineProps<{ form: GeneralForm<'usage'> }>()

const announce = useAnnouncer()
const r = props.form
const root = ref<HTMLElement>()
const ids = useId()
const labelId = `${ids}-windows`
const groupErrorId = `${ids}-windows-error`

useLeaveGuard(r.dirty)

const usage = computed(() => r.draft.value?.usage ?? null)
const fe = computed(() => r.fieldErrors.value)
const unplaced = computed(() => unplacedErrors(fe.value, placedLimitField))

// The windows are edited as rows of text; the draft holds the map they make.
// The rows are built again when the draft's map changes from outside the
// rows (a read, Discard, "Use the new value").
const rows = ref<OverrideRow[]>(rowsOf(usage.value?.windows))
const problems = ref<Record<string, string>>({})
let reported = new Set<string>()

function rebuild() {
  rows.value = rowsOf(usage.value?.windows)
}
watch(
  () => usage.value?.windows,
  (now) => {
    if (now === undefined) return
    const fromRows = windowsOf(rows.value)
    if (Object.keys(fromRows.problems).length > 0 || !sameJSON(fromRows.windows, now)) rebuild()
  },
  { deep: true },
)
watch(r.resets, rebuild)
watch(
  rows,
  (now) => {
    const { windows, problems: found } = windowsOf(now)
    problems.value = found
    const keys = new Set(Object.keys(found).map((k) => `windows.${k}`))
    for (const k of reported) if (!keys.has(k)) r.setParse(k, null)
    for (const [k, m] of Object.entries(found)) r.setParse(`windows.${k}`, m)
    reported = keys
    const d = r.draft.value
    if (d && Object.keys(found).length === 0 && !sameJSON(d.usage.windows, windows)) d.usage.windows = windows as Record<string, UsageThreshold>
  },
  { deep: true },
)

const cellError = (i: number, which: 'soft' | 'hard') => problems.value[`${i}.${which}`] ?? overrideError(fe.value, rows.value[i]?.name ?? '', which)
const nameError = (i: number) => problems.value[`${i}.name`]
const shown = (name: string) => visibleText(name.trim()) || `row without a name`

function addRow() {
  rows.value.push({ name: '', soft: '', hard: '' })
  announce.polite('Window added at the end.')
}
function removeRow(i: number) {
  const gone = rows.value.splice(i, 1)[0]
  if (gone) announce.polite(`Limits of ${shown(gone.name)} removed. Save to apply.`)
}

async function save() {
  if ((await r.save()) === 'refused') await focusFirstInvalid(root.value)
}
</script>

<template>
  <FormSection legend="Limits" description="A model's usage is the fullest window that counts for it.">
    <div ref="root" class="usage-limits" data-usage-limits>
      <FileErrors :errors="r.broken.value" />
      <Callout v-if="r.changedOnDisk.value" tone="warn" live="polite" data-changed-on-disk>
        <p>{{ diskChangeText(r.changedOnDisk.value) }}</p>
      </Callout>
      <p v-if="r.loading.value && !usage" class="usage-limits__note">Reading the limits…</p>
      <Callout v-else-if="!usage && r.error.value" tone="danger" live="assertive" data-settings-load-error>
        <p>{{ r.error.value }}</p>
        <template #actions><UiButton size="sm" @click="r.reload()">Try again</UiButton></template>
      </Callout>

      <template v-if="usage">
        <UnplacedErrors :errors="unplaced" />
        <div class="usage-limits__pair">
          <NumberField
            v-model="usage.soft_percent"
            label="Soft limit, %"
            :min="1"
            :max="100"
            required
            hint="From here no new agent starts on the model."
            :error="fe['usage.soft_percent']"
            data-field="usage.soft_percent"
          />
          <NumberField
            v-model="usage.hard_percent"
            label="Hard limit, %"
            :min="1"
            :max="100"
            required
            hint="From here running agents hand over and stop."
            :error="fe['usage.hard_percent']"
            data-field="usage.hard_percent"
          />
        </div>
        <DurationField
          v-model="usage.refresh_interval"
          label="Refresh every"
          hint="How often omo runs the script of each account."
          :error="fe['usage.refresh_interval']"
          data-field="usage.refresh_interval"
        />

        <div class="omo-field usage-over">
          <p :id="labelId" class="omo-field__label">Limits per window</p>
          <div role="group" :aria-labelledby="labelId" :aria-describedby="fe['usage.windows'] ? groupErrorId : undefined" class="usage-over__group">
            <div v-if="rows.length > 0" class="usage-over__head" aria-hidden="true">
              <span>Window</span>
              <span>Soft limit</span>
              <span>Hard limit</span>
            </div>
            <ul v-if="rows.length > 0" class="usage-over__rows">
              <li v-for="(o, i) in rows" :key="i" class="usage-over__row" :data-window-row="o.name.trim()">
                <div class="usage-over__cell usage-over__cell--name">
                  <span class="usage-over__cell-label" aria-hidden="true">Window</span>
                  <input
                    v-model="o.name"
                    class="omo-input"
                    :aria-label="`Window ${i + 1}`"
                    :aria-invalid="nameError(i) ? 'true' : undefined"
                    :aria-describedby="nameError(i) ? `${ids}-${i}-name` : undefined"
                    spellcheck="false"
                    autocomplete="off"
                    autocapitalize="off"
                    data-window-name
                  />
                  <p v-if="nameError(i)" :id="`${ids}-${i}-name`" class="omo-field__error">{{ nameError(i) }}</p>
                </div>
                <div v-for="which in ['soft', 'hard'] as const" :key="which" class="usage-over__cell">
                  <span class="usage-over__cell-label" aria-hidden="true">{{ which === 'soft' ? 'Soft limit' : 'Hard limit' }}</span>
                  <input
                    v-model="o[which]"
                    class="omo-input"
                    inputmode="decimal"
                    autocomplete="off"
                    spellcheck="false"
                    :aria-label="`${which === 'soft' ? 'Soft' : 'Hard'} limit of ${shown(o.name)}`"
                    :placeholder="String(which === 'soft' ? usage.soft_percent : usage.hard_percent)"
                    :aria-invalid="cellError(i, which) ? 'true' : undefined"
                    :aria-describedby="cellError(i, which) ? `${ids}-${i}-${which}` : undefined"
                    :data-window-limit="which"
                  />
                  <p v-if="cellError(i, which)" :id="`${ids}-${i}-${which}`" class="omo-field__error">{{ cellError(i, which) }}</p>
                </div>
                <IconButton :label="`Remove the limits of ${shown(o.name)}`" class="usage-over__remove" @click="removeRow(i)">
                  <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                    <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
                  </svg>
                </IconButton>
              </li>
            </ul>
            <div>
              <UiButton size="sm" data-add-window @click="addRow">
                <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
                  <path d="M6 1.5v9M1.5 6h9" stroke="currentColor" stroke-width="1.5" />
                </svg>
                Add window
              </UiButton>
            </div>
          </div>
          <p class="omo-field__hint">In percent; an empty field uses the limit above. Window names come from the scripts.</p>
          <p v-if="fe['usage.windows']" :id="groupErrorId" class="omo-field__error">{{ fe['usage.windows'] }}</p>
        </div>

        <SaveBar
          label="Save changes to the limits"
          :dirty="r.dirty.value"
          :busy="r.saving.value"
          :status="r.status.value"
          :error="r.error.value || undefined"
          @save="save"
          @discard="r.discard()"
        >
          <template v-if="r.status.value === 'conflict'">
            <UiButton size="sm" data-use-theirs @click="r.useTheirs()">Use the new value</UiButton>
            <UiButton size="sm" data-keep-mine @click="r.keepMine()">Keep mine</UiButton>
          </template>
        </SaveBar>
      </template>
    </div>
  </FormSection>
</template>

<style scoped>
.usage-limits {
  display: grid;
  gap: calc(var(--omo-space) * 5);
  min-width: 0;
}
.usage-limits__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.usage-limits__pair {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(calc(var(--omo-space) * 50), 1fr));
  gap: calc(var(--omo-space) * 4);
}

/* Limits per window: the same table as the environment's, stacked when narrow. */
.usage-over {
  container-type: inline-size;
}
.usage-over__group {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.usage-over__head,
.usage-over__row {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 1fr) minmax(0, 1fr) var(--omo-control-h);
  align-items: start;
  column-gap: calc(var(--omo-space) * 2);
}
.usage-over__head {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.usage-over__rows {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.usage-over__cell {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
}
.usage-over__remove {
  align-self: start;
}
/* Wide, the head names the columns; narrow, each field has its own small label. */
.usage-over__cell-label {
  display: none;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@container (max-width: 479.98px) {
  .usage-over__head {
    display: none;
  }
  .usage-over__row {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) var(--omo-control-h);
    align-items: end;
    row-gap: calc(var(--omo-space) * 2);
    padding-bottom: calc(var(--omo-space) * 2);
    border-bottom: var(--omo-border) solid var(--omo-line);
  }
  .usage-over__cell-label {
    display: block;
  }
  .usage-over__cell--name {
    grid-column: 1 / -1;
  }
  .usage-over__remove {
    align-self: end;
  }
}
</style>
