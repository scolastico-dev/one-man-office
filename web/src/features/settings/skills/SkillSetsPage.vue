<script setup lang="ts">
// The Skill sets section of the settings: the git repositories of skills omo
// keeps one read-only copy of, each with its state, and one open form at a
// time. The list and the states are read again when a skill set file or a
// checkout changes, after a snapshot and when the event stream is live again;
// reads are numbered so an older answer never replaces a newer one. The open
// set is part of this page's state, not of the address.
import { computed, onScopeDispose, ref, useId, watch } from 'vue'
import { getSkillSets } from '../../../api/endpoints'
import type { ApiEvent, FileError, SettingsChangedData, SettingsInvalidData, SkillSet, SkillSetState } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import FileErrors from '../FileErrors.vue'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { focusAfterClose, rowButton, useOpenEditor } from '../openEditor'
import SkillSetEditor from './SkillSetEditor.vue'
import SkillSetList from './SkillSetList.vue'
import SkillSetNameDialog from './SkillSetNameDialog.vue'
import { blankSkillSet, fileOfSkillSet, freeName, hasSuperpowers, superpowersPreset, SUPERPOWERS_PRESET, type SkillDraft } from './skillForms'

const def = sectionDef('skills')
const session = useSession()
const store = useBoard()

const sets = ref<SkillSet[]>([])
const states = ref<SkillSetState[]>([])
const errors = ref<FileError[]>([])
const loaded = ref(false)
const loadError = ref('')

let seq = 0
let applied = 0
async function reload() {
  const mine = ++seq
  try {
    const { data } = await getSkillSets(session.client)
    if (mine <= applied) return
    applied = mine
    sets.value = data.skill_sets
    states.value = data.states
    errors.value = data.errors
    loadError.value = ''
  } catch (e) {
    if (mine <= applied) return
    loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The skill sets could not be read.'
  } finally {
    loaded.value = true
  }
}

/** A state the server answered with (Update now): newer than every read under way. */
function takeState(st: SkillSetState) {
  applied = ++seq
  states.value = [...states.value.filter((s) => s.name !== st.name), st]
}

function concernsSkillSets(ev: ApiEvent): boolean {
  if (ev.kind === 'skillset.changed') return true
  if (ev.kind === 'settings.changed') return (ev.data as Partial<SettingsChangedData> | null)?.section === 'skillsets'
  if (ev.kind === 'settings.invalid') {
    const f = (ev.data as Partial<SettingsInvalidData> | null)?.file
    return typeof f === 'string' && fileOfSkillSet(f) !== null
  }
  return false
}
const offEvent = store.onEvent('*', (ev) => {
  if (concernsSkillSets(ev)) void reload()
})
const offSnapshot = store.onSnapshot(() => void reload())
const stopLive = watch(
  () => store.connection.value,
  (now, before) => {
    if (now === 'live' && before !== 'live') void reload()
  },
)
onScopeDispose(() => {
  offEvent()
  offSnapshot()
  stopLive()
})
void reload()

/** The open set, and for one not stored yet where its form starts. */
const selected = ref<string | null>(null)
const initial = ref<SkillDraft | null>(null)
const editor = useOpenEditor(() => `the skill set ${visibleText(selected.value ?? '')}`)
const formId = useId()

const names = computed(() => sets.value.map((s) => s.name))
/** Broken files of sets whose form is not open (the open form shows its own). */
const listErrors = computed(() => errors.value.filter((e) => fileOfSkillSet(e.file) !== selected.value))
const offerSuperpowers = computed(() => loaded.value && !hasSuperpowers(sets.value))

async function toggle(name: string) {
  if (!(await editor.leave())) return
  selected.value = selected.value === name ? null : name
  initial.value = null
}
async function openNew(name: string, start: SkillDraft) {
  if (!(await editor.leave())) return
  selected.value = name
  initial.value = start
}
const newButton = ref<InstanceType<typeof UiButton>>()
/** Focus after the form of `name` went away: its row, else Add skill set…. */
const focusAfter = (name: string) =>
  focusAfterClose(
    () => rowButton('data-skillset', name, 'data-skillset-toggle'),
    () => newButton.value?.$el as HTMLElement | undefined,
  )
async function closeNew() {
  if (!(await editor.leave())) return
  const name = selected.value
  selected.value = null
  initial.value = null
  if (name !== null && selected.value === null) await focusAfter(name)
}
async function deleted() {
  const name = selected.value
  editor.reset()
  selected.value = null
  initial.value = null
  await reload()
  if (name !== null && selected.value === null) await focusAfter(name)
}

const naming = ref(false)
const stateOfOpen = computed(() => states.value.find((s) => s.name === selected.value) ?? null)
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <FormSection legend="Skill sets" description="Git repositories of skills. omo keeps one read-only copy of each and tells the agents of each role where it is.">
      <FileErrors :errors="listErrors" />
      <Callout v-if="loadError" tone="danger" live="assertive" data-skillsets-error>
        <p>{{ loadError }}</p>
        <template #actions><UiButton size="sm" @click="reload()">Try again</UiButton></template>
      </Callout>
      <p v-if="!loaded" class="skills__note">Reading the skill sets…</p>
      <p v-else-if="sets.length === 0 && !loadError" class="skills__note" data-no-skillsets>No skill set yet.</p>
      <SkillSetList v-else :sets="sets" :states="states" :selected="selected" :controls="formId" @select="toggle" />
      <div class="skills__buttons">
        <UiButton ref="newButton" data-new-skillset @click="naming = true">Add skill set…</UiButton>
        <UiButton v-if="offerSuperpowers" data-add-superpowers @click="openNew(freeName(SUPERPOWERS_PRESET.name, names), superpowersPreset(freeName(SUPERPOWERS_PRESET.name, names)))">
          Add Superpowers
        </UiButton>
      </div>

      <Callout v-if="editor.busyNote.value" tone="warn" data-editor-busy>
        <p>{{ editor.busyNote.value }}</p>
      </Callout>
      <SkillSetEditor
        v-if="selected !== null"
        :key="selected"
        :name="selected"
        :initial="initial"
        :state="stateOfOpen"
        :form-id="formId"
        @dirty="(d) => (editor.dirty.value = d)"
        @busy="(b) => (editor.busy.value = b)"
        @deleted="deleted"
        @close="closeNew"
        @state="takeState"
      />
    </FormSection>

    <SkillSetNameDialog :open="naming" :taken="names" @close="naming = false" @create="(n) => openNew(n, blankSkillSet(n))" />
  </SettingsPage>
</template>

<style scoped>
.skills__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.skills__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
</style>
