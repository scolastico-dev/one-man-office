<script setup lang="ts">
// The workspace folders: where omo looks for repositories. One form over
// `workspace_roots` of the settings (the page's Save bar saves it); every
// row has its path (typed, or chosen with Browse…), a search depth and
// ignore patterns. A new folder is chosen in the folder picker first and
// starts with the default ignore patterns.
import { computed, nextTick, ref } from 'vue'
import FormSection from '../../../components/ui/FormSection.vue'
import ListField from '../../../components/ui/ListField.vue'
import NumberField from '../../../components/ui/NumberField.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import UnplacedErrors from '../UnplacedErrors.vue'
import { unplacedErrors } from '../unplacedErrors'
import type { SettingsResource } from '../useSettingsResource'
import FolderPickerDialog from './FolderPickerDialog.vue'
import { useFolderPicker } from './useFolderPicker'
import { newRoot, type RootsForm } from './useProjects'

const props = defineProps<{ resource: SettingsResource<RootsForm> }>()

const announce = useAnnouncer()
const picker = useFolderPicker()
const box = ref<HTMLElement>()

const roots = computed(() => props.resource.draft.value?.workspace_roots ?? [])
const errors = computed(() => props.resource.fieldErrors.value)

const fieldError = (i: number, part: string) => errors.value[`workspace_roots.${i}.${part}`]
/** The fields of the form: the list, and each folder's path, depth, patterns and single patterns. */
const PLACED = /^workspace_roots(\.\d+\.(path|max_depth|ignore(\.\d+)?))?$/
const unplaced = computed(() => unplacedErrors(errors.value, (f) => PLACED.test(f) && rowExists(f)))
/** A refusal of a folder or pattern that is no longer in the form has no field to sit under. */
function rowExists(field: string): boolean {
  const m = /^workspace_roots\.(\d+)(?:\.ignore\.(\d+))?/.exec(field)
  if (!m) return true
  const root = roots.value[Number(m[1])]
  return root !== undefined && (m[2] === undefined || Number(m[2]) < root.ignore.length)
}

/** Messages of single patterns of root `i`, by index. */
function patternErrors(i: number): Record<number, string> {
  const out: Record<number, string> = {}
  const prefix = `workspace_roots.${i}.ignore.`
  for (const [k, v] of Object.entries(errors.value)) {
    if (k.startsWith(prefix)) {
      const n = Number(k.slice(prefix.length))
      if (Number.isInteger(n)) out[n] = v
    }
  }
  return out
}

async function focusIn(i: number, selector: string) {
  await nextTick()
  box.value?.querySelector<HTMLElement>(`[data-root="${i}"] ${selector}`)?.focus()
}

async function add() {
  const path = await picker.choose({ title: 'Choose a workspace folder' })
  const draft = props.resource.draft.value
  if (path === null || draft === null) return
  draft.workspace_roots.push(newRoot(path))
  const n = draft.workspace_roots.length
  announce.polite(`Folder ${n} added. Save to look for repositories in it.`)
  await focusIn(n - 1, '[data-root-path]')
}

async function browse(i: number) {
  const root = roots.value[i]
  if (!root) return
  const path = await picker.choose({ title: `Choose workspace folder ${i + 1}`, start: root.path })
  const now = props.resource.draft.value?.workspace_roots[i]
  if (path === null || !now) return
  now.path = path
  await focusIn(i, '[data-root-path]')
}

async function remove(i: number) {
  const draft = props.resource.draft.value
  if (!draft) return
  draft.workspace_roots.splice(i, 1)
  announce.polite(`Folder ${i + 1} removed. Save to stop looking there; its projects stay.`)
  await nextTick()
  const next = box.value?.querySelector<HTMLElement>(`[data-root="${Math.min(i, draft.workspace_roots.length - 1)}"] [data-root-remove]`)
  ;(next ?? box.value?.querySelector<HTMLElement>('[data-root-add]'))?.focus()
}

/**
 * Adds `pattern` to the ignore patterns of the folder written as `path` and
 * puts the focus on it. Returns the folder's number, or null when the form
 * has no such folder.
 */
async function addIgnore(path: string, pattern: string): Promise<number | null> {
  const draft = props.resource.draft.value
  if (!draft) return null
  const i = draft.workspace_roots.findIndex((r) => r.path === path)
  const root = draft.workspace_roots[i]
  if (!root) return null
  let at = root.ignore.indexOf(pattern)
  if (at < 0) {
    root.ignore.push(pattern)
    at = root.ignore.length - 1
  }
  await focusIn(i, `[data-row="${at}"] [data-part="input"]`)
  return i + 1
}

defineExpose({ addIgnore })
</script>

<template>
  <FormSection
    legend="Workspace folders"
    description="Repositories found here become projects at once. Folders inside a repository are not searched; links are not followed."
  >
    <div ref="box" class="roots" data-workspace-roots>
      <UnplacedErrors :errors="unplaced" />
      <p v-if="roots.length === 0" class="roots__empty">No workspace folder yet. Projects can still be added one by one.</p>
      <div v-for="(root, i) in roots" :key="i" class="roots__root" :data-root="i">
        <div class="roots__path">
          <TextField
            v-model="root.path"
            :label="`Folder ${i + 1}`"
            :error="fieldError(i, 'path')"
            spellcheck="false"
            autocomplete="off"
            data-root-path
          />
          <UiButton class="roots__browse" :aria-label="`Browse… for folder ${i + 1}`" data-root-browse @click="browse(i)">Browse…</UiButton>
        </div>
        <NumberField
          v-model="root.max_depth"
          label="Search depth"
          :min="1"
          :max="16"
          required
          hint="How many folders deep to look, 1 to 16."
          :error="fieldError(i, 'max_depth')"
          data-root-depth
        />
        <ListField
          v-model="root.ignore"
          label="Ignore"
          item-label="Pattern"
          add-label="Add pattern"
          empty-text="Nothing is ignored."
          hint="A folder name such as node_modules, or a path below the folder such as archive/*."
          :error="fieldError(i, 'ignore')"
          :errors="patternErrors(i)"
          data-root-ignore
        />
        <div>
          <UiButton size="sm" variant="danger" data-root-remove @click="remove(i)">Remove folder {{ i + 1 }}</UiButton>
        </div>
      </div>
      <p v-if="errors.workspace_roots" class="omo-field__error" data-roots-error>{{ errors.workspace_roots }}</p>
      <div>
        <UiButton data-root-add @click="add">Add workspace folder…</UiButton>
      </div>
    </div>
    <FolderPickerDialog v-bind="picker.dialog.value" @choose="picker.chosen" @close="picker.closed" />
  </FormSection>
</template>

<style scoped>
.roots {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
}
.roots__empty {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.roots__root {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
}
.roots__path {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the input below a one-line label. */
.roots__browse {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
  min-height: calc(var(--omo-space) * 10);
}
</style>
