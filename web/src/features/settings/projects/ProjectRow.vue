<script setup lang="ts">
// One project in the list: name, folder, branch, where it came from and its
// state, the switch that lets new tickets use it, and behind "Edit" its name,
// its merge mode for each kind of merge (its own, or the general default) and
// Remove…. Each change is saved on its own, like a ticket field; the list
// shows the stored project as the stream brings it.
// A refusal is shown in the row in the server's words. When the server
// refuses to remove a project a workspace folder would find again, the row
// offers the two ways out it names: switch it off, or ignore it in the folder.
import { computed, nextTick, ref, useId, watch } from 'vue'
import { ApiError } from '../../../api/client'
import { removeProject, updateProject } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { MergeModes, Project, UpdateProject } from '../../../api/types'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
import Toggle from '../../../components/ui/Toggle.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { branchNotice } from '../../create/branchNotice'
import { projectState } from '../../projects/projectState'
import { MERGE_KINDS, modeWords, overridesMerge, overrideText, projectModeOptions, type MergeKind } from './mergeModes'
import { originText } from './useProjects'

const props = withDefaults(
  defineProps<{
    project: Project
    /** The general merge defaults as last read; null while unknown. */
    defaults?: MergeModes | null
    /** The list is in its multi-select mode: the row starts with a box. */
    selecting?: boolean
    selected?: boolean
  }>(),
  { defaults: null, selecting: false, selected: false },
)
const emit = defineEmits<{
  /** The box was clicked: `on` is its new state; `shift` asks for a range. */
  pick: [shift: boolean, on: boolean]
  /** Asks the page to add an ignore pattern for this project to its workspace folder. */
  ignore: [project: Project]
}>()

const session = useSession()
const announce = useAnnouncer()
const editId = useId()

const p = computed(() => props.project)
const shownName = computed(() => visibleText(p.value.name))
const state = computed(() => projectState({ ...p.value, enabled: enabledShown.value }))

const editing = ref(false)
const nameDraft = ref(p.value.name)
const busy = ref(false)
const message = ref('')
/** The refusal was the one for a project a workspace folder would find again. */
const waysOut = ref(false)
const confirming = ref(false)
/** The switch's value while its change is on the way. */
const enabledWanted = ref<boolean | null>(null)
const enabledShown = computed(() => enabledWanted.value ?? p.value.enabled)
/** The server's refusal of a merge mode, by kind, shown under its select. */
const mergeErrors = ref<Partial<Record<MergeKind, string>>>({})
const overridden = computed(() => overridesMerge(p.value))
const columns = computed(() => (props.selecting ? 6 : 5))

function onPick(e: MouseEvent) {
  const box = e.target as HTMLInputElement
  const on = !props.selected
  // The list decides; the box shows what it decided even when its value did not change.
  box.checked = on
  emit('pick', e.shiftKey, on)
}

// A new version of the project (from the stream) is what is stored now.
watch(
  () => props.project,
  (now, before) => {
    if (enabledWanted.value !== null && now.enabled === enabledWanted.value) enabledWanted.value = null
    if (!editing.value || nameDraft.value === before?.name) nameDraft.value = now.name
  },
)

function say(text: string, ways = false) {
  message.value = text
  waysOut.value = ways
  announce.assertive(text)
}

function clearMessage() {
  message.value = ''
  waysOut.value = false
}

/**
 * Saves `patch`; a refusal is shown in the row, or under the field when the
 * server names `field` (a dotted key) as the one it refused.
 */
async function send(patch: UpdateProject, done: string, field?: string): Promise<boolean> {
  busy.value = true
  clearMessage()
  try {
    await updateProject(session.client, p.value.id, patch)
    announce.polite(done)
    return true
  } catch (e) {
    const named = e instanceof ApiError ? (e.details as { field?: unknown } | undefined)?.field : undefined
    if (field !== undefined && named === field) {
      mergeErrors.value = { ...mergeErrors.value, [field.slice('merge.'.length)]: e instanceof Error ? visibleText(e.message) : '' }
      announce.assertive((e as Error).message)
      return false
    }
    say(
      visibleText(
        failureText(e, {
          lost: 'No answer from omo, so the change may have been saved. The list shows what omo stored.',
          refused: 'The change was refused.',
        }),
      ),
    )
    return false
  } finally {
    busy.value = false
  }
}

async function setEnabled(on: boolean) {
  enabledWanted.value = on
  const ok = await send({ enabled: on }, on ? `${shownName.value} is switched on.` : `${shownName.value} is switched off. New tickets cannot use it.`)
  if (!ok || p.value.enabled === on) enabledWanted.value = null
}

async function rename() {
  if (busy.value) return
  const name = nameDraft.value.trim()
  if (name === p.value.name) {
    editing.value = false
    return
  }
  const ok = await send({ name }, `Renamed to ${visibleText(name)}.`)
  // A refused name goes back to the stored one; the message says why.
  if (!ok) nameDraft.value = p.value.name
}

function resetName() {
  nameDraft.value = p.value.name
}

async function setMerge(kind: MergeKind, value: string) {
  const stored = p.value.merge[kind]
  if (value === stored) return
  const { [kind]: _, ...rest } = mergeErrors.value
  mergeErrors.value = rest
  const label = MERGE_KINDS.find((k) => k.kind === kind)!.label
  const done = value === '' ? `${label}: the global default.` : `${label}: ${modeWords(value)}.`
  const ok = await send({ merge: { [kind]: value as '' | 'merge' | 'squash' } }, done, `merge.${kind}`)
  // A refused mode: the select shows the stored one again; the reason stays under it.
  if (!ok) {
    const el = document.getElementById(editId)?.querySelector<HTMLSelectElement>(`select[data-project-merge="${kind}"]`)
    if (el) el.value = p.value.merge[kind]
  }
}

async function toggleEdit() {
  editing.value = !editing.value
  confirming.value = false
  mergeErrors.value = {}
  nameDraft.value = p.value.name
  if (editing.value) {
    await nextTick()
    document.getElementById(editId)?.querySelector<HTMLElement>('input')?.focus()
  }
}

async function askRemove() {
  confirming.value = true
  await nextTick()
  document.getElementById(editId)?.querySelector<HTMLElement>('[data-project-remove-confirm]')?.focus()
}

async function remove() {
  if (busy.value) return
  busy.value = true
  clearMessage()
  try {
    await removeProject(session.client, p.value.id)
    announce.polite(`${shownName.value} removed.`)
  } catch (e) {
    confirming.value = false
    const text = visibleText(
      failureText(e, {
        lost: 'No answer from omo, so the project may have been removed. The list shows what omo stored.',
        refused: 'The project could not be removed.',
      }),
    )
    say(text, e instanceof ApiError && e.status === 422 && p.value.root !== '')
  } finally {
    busy.value = false
  }
}

async function switchOff() {
  await setEnabled(false)
}

</script>

<template>
  <tr :data-project="p.id" :data-state="state">
    <td v-if="selecting" class="projects__pick">
      <input type="checkbox" class="projects__box" :checked="selected" :aria-label="`Select ${shownName}`" :data-project-select="p.id" @click="onPick" />
    </td>
    <th scope="row" data-project-name>{{ shownName }}</th>
    <td class="projects__path">
      <span class="projects__folder">{{ visibleText(p.path) }}</span>
      <span class="projects__origin" data-project-origin>{{ visibleText(originText(p)) }}</span>
      <span v-if="overridden" class="projects__override" :title="`Its own merge mode: ${overrideText(p)}`" data-project-merge-override>merge: overridden</span>
      <span v-if="p.missing" class="projects__missing" data-project-missing>The folder is gone. New tickets cannot use it.</span>
    </td>
    <td class="projects__branch">
      <code v-if="p.current_branch">{{ visibleText(p.current_branch) }}</code>
      <span v-if="branchNotice(p)" class="omo-field__hint projects__notice" data-branch-notice>
        Not its default branch <code>{{ visibleText(p.default_branch ?? '') }}</code>.
      </span>
    </td>
    <td class="projects__use">
      <span class="projects__state" :data-state="state">{{ state }}</span>
      <Toggle
        v-if="!p.missing"
        size="sm"
        :model-value="enabledShown"
        :label="`Use ${shownName}`"
        hide-label
        :disabled="busy"
        data-project-enabled
        @update:model-value="setEnabled"
      />
    </td>
    <td class="projects__actions">
      <UiButton size="sm" variant="ghost" :aria-label="`Edit ${shownName}`" :aria-expanded="editing ? 'true' : 'false'" :aria-controls="editId" data-project-edit @click="toggleEdit">
        Edit
      </UiButton>
    </td>
  </tr>
  <tr v-if="editing" class="projects__more" :data-project-more="p.id">
    <td :id="editId" :colspan="columns">
      <div class="projects__form">
        <TextField
          v-model="nameDraft"
          label="Name"
          hint="Enter saves, Escape puts the stored name back."
          autocomplete="off"
          spellcheck="false"
          :disabled="busy"
          data-project-rename
          @keydown.enter.prevent="rename"
          @keydown.esc.prevent="resetName"
        />
        <SelectField
          v-for="k in MERGE_KINDS"
          :key="k.kind"
          :model-value="p.merge[k.kind]"
          :label="k.label"
          :options="projectModeOptions(p, k.kind, defaults)"
          :hint="k.hint"
          :error="mergeErrors[k.kind]"
          :disabled="busy"
          :data-project-merge="k.kind"
          @update:model-value="(v) => setMerge(k.kind, v)"
        />
        <div v-if="!confirming">
          <UiButton size="sm" variant="danger" :disabled="busy" data-project-remove @click="askRemove">Remove…</UiButton>
        </div>
        <div v-else class="projects__confirm" role="group" :aria-label="`Remove ${shownName}?`">
          <p class="m-0">Remove {{ shownName }} from omo? The folder stays as it is; tickets keep their history.</p>
          <div class="projects__buttons">
            <UiButton size="sm" variant="danger" :disabled="busy" data-project-remove-confirm @click="remove">Remove</UiButton>
            <UiButton size="sm" :disabled="busy" @click="confirming = false">Cancel</UiButton>
          </div>
        </div>
      </div>
    </td>
  </tr>
  <tr v-if="message" class="projects__more" :data-project-message="p.id">
    <td :colspan="columns">
      <div class="projects__message">
        <p class="m-0" data-project-error>{{ message }}</p>
        <div v-if="waysOut" class="projects__buttons">
          <UiButton v-if="p.enabled" size="sm" :disabled="busy" data-project-switch-off @click="switchOff">Switch it off</UiButton>
          <UiButton size="sm" :disabled="busy" data-project-ignore @click="emit('ignore', p)">Add an ignore pattern</UiButton>
        </div>
      </div>
    </td>
  </tr>
</template>
