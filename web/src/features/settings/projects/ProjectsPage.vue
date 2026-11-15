<script setup lang="ts">
// The Projects section of the settings: the workspace folders omo searches
// for repositories (a form, saved with the page's Save bar), the projects it
// knows (each change saved on its own) and the state of the last scan.
// Names and paths can be written by agents (a project file is a file in
// omo's home), so they are shown as visible text.
import { computed, ref } from 'vue'
import { listFolders } from '../../../api/endpoints'
import type { Project } from '../../../api/types'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { useSettingsResource, type SettingsResource } from '../useSettingsResource'
import ProjectList from './ProjectList.vue'
import ScanStatus from './ScanStatus.vue'
import { pathBelow, rootsResourceOptions } from './useProjects'
import WorkspaceRoots from './WorkspaceRoots.vue'

const def = sectionDef('projects')
const session = useSession()
const announce = useAnnouncer()

const roots = useSettingsResource(rootsResourceOptions(session.client))
const page = roots as SettingsResource<unknown>
const storedRoots = computed(() => roots.value.value?.workspace_roots.length ?? null)
const rootsForm = ref<InstanceType<typeof WorkspaceRoots>>()

/** The user's home folder, for workspace folders written as `~/…`; read once when needed. */
let home: string | null = null
async function homeFolder(): Promise<string | null> {
  if (home !== null) return home
  try {
    home = (await listFolders(session.client)).data.home
  } catch {
    return null
  }
  return home
}

/** The way out of a refused removal: ignore the project in the workspace folder that finds it. */
async function addIgnore(p: Project) {
  const root = p.root
  const rel = pathBelow(p.path, root, root.startsWith('~') ? await homeFolder() : null)
  if (rel === null) {
    announce.assertive(`${visibleText(p.path)} does not lie below ${visibleText(root)}; add the pattern by hand.`)
    return
  }
  const n = await rootsForm.value?.addIgnore(root, rel)
  if (n === null || n === undefined) {
    announce.assertive(`The workspace folder ${visibleText(root)} is not in the list any more.`)
    return
  }
  announce.polite(`Pattern ${visibleText(rel)} added to folder ${n}. Save, then remove the project again.`)
}
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" :resource="page">
    <template #actions>
      <ScanStatus :roots="storedRoots" />
    </template>
    <WorkspaceRoots ref="rootsForm" :resource="roots" />
    <ProjectList @ignore="addIgnore" />
  </SettingsPage>
</template>
