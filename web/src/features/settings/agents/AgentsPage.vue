<script setup lang="ts">
// The Agents section of the settings: the agent profiles (which program runs,
// with which command, arguments, environment and account) and their models,
// one profile's form at a time, and the detection of agent programs. The open
// profile is part of this page's state, not of the address. The list is read
// again (liveRead) when a profile file changes, after a snapshot, when the
// event stream is live again and when the tab is shown again.
import { computed, ref } from 'vue'
import { getProfiles } from '../../../api/endpoints'
import type { ApiEvent, FileError, Profile, SettingsChangedData, SettingsInvalidData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { liveRead } from '../../../store/liveRead'
import FileErrors from '../FileErrors.vue'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { askFirst, LEAVE_QUESTION } from '../useLeaveGuard'
import DetectButton from './DetectButton.vue'
import DuplicateDialog from './DuplicateDialog.vue'
import ProfileEditor from './ProfileEditor.vue'
import ProfileList from './ProfileList.vue'
import { fileOfProfile } from './profileForm'

const def = sectionDef('agents')
const session = useSession()
const store = useBoard()

const profiles = ref<Profile[]>([])
const errors = ref<FileError[]>([])
const loaded = ref(false)
const loadError = ref('')

function concernsProfiles(ev: ApiEvent): boolean {
  if (ev.kind === 'settings.changed') return (ev.data as Partial<SettingsChangedData> | null)?.section === 'profiles'
  if (ev.kind === 'settings.invalid') {
    const f = (ev.data as Partial<SettingsInvalidData> | null)?.file
    return typeof f === 'string' && fileOfProfile(f) !== null
  }
  return false
}
const live = liveRead({
  store,
  read: async () => (await getProfiles(session.client)).data,
  apply(data) {
    profiles.value = data.profiles
    errors.value = data.errors
    loadError.value = ''
    loaded.value = true
  },
  failed(e) {
    loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The profiles could not be read.'
    loaded.value = true
  },
  watches: concernsProfiles,
})
const reload = live.reload

/** The open profile, and for one not stored yet the program chosen for it. */
const selected = ref<string | null>(null)
const newCli = ref<string | null>(null)
const editorDirty = ref(false)

/** Broken files of profiles whose form is not open (the open form shows its own). */
const listErrors = computed(() => errors.value.filter((e) => fileOfProfile(e.file) !== selected.value))
const names = computed(() => profiles.value.map((p) => p.name))

async function open(name: string, cli: string | null = null) {
  if (name === selected.value && cli === null) return
  if (editorDirty.value && !(await askFirst(LEAVE_QUESTION))) return
  editorDirty.value = false
  deletedText.value = ''
  selected.value = name
  newCli.value = cli
}

const naming = ref(false)
const copyOf = ref<Profile | null>(null)
function startNew() {
  copyOf.value = null
  naming.value = true
}
function startDuplicate(p: Profile) {
  copyOf.value = p
  naming.value = true
}
async function duplicated(name: string) {
  await reload()
  await open(name)
}
/** What the last delete did, until another profile is opened. */
const deletedText = ref('')
function deleted(text: string) {
  deletedText.value = text
  editorDirty.value = false
  selected.value = null
  newCli.value = null
  void reload()
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <template #actions>
      <UiButton variant="primary" data-new-profile @click="startNew">New profile…</UiButton>
    </template>

    <FormSection legend="Profiles" description="A profile is one agent program with its command, arguments, environment and account; its models are what roles use.">
      <FileErrors :errors="listErrors" />
      <Callout v-if="deletedText" tone="info" live="polite" data-profile-deleted>
        <p>{{ deletedText }}</p>
      </Callout>
      <Callout v-if="loadError" tone="danger" live="assertive" data-profiles-error>
        <p>{{ loadError }}</p>
        <template #actions><UiButton size="sm" @click="reload()">Try again</UiButton></template>
      </Callout>
      <p v-if="!loaded" class="agents__note">Reading the profiles…</p>
      <p v-else-if="profiles.length === 0 && !loadError" class="agents__note" data-no-profiles>
        No profile yet. Create one, or let omo detect the agent programs on this computer.
      </p>
      <ProfileList v-else :profiles="profiles" :selected="selected" @select="(n) => open(n)" />
    </FormSection>

    <ProfileEditor
      v-if="selected !== null"
      :key="selected"
      :name="selected"
      :new-cli="newCli"
      @dirty="(d) => (editorDirty = d)"
      @deleted="deleted"
      @duplicate="startDuplicate"
    />

    <FormSection legend="Detect agent programs">
      <DetectButton @detected="reload()" />
    </FormSection>

    <DuplicateDialog
      :open="naming"
      :source="copyOf"
      :taken="names"
      @close="naming = false"
      @duplicated="duplicated"
      @create="(n, cli) => open(n, cli)"
    />
  </SettingsPage>
</template>

<style scoped>
.agents__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
