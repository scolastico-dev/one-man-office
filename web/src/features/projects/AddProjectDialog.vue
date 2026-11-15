<script setup lang="ts">
// Adds a project by the path of its folder, typed or chosen with Browse… in
// the folder picker. The server decides whether the folder is a usable git
// repository; its answer is shown next to the field.
import { defineAsyncComponent, ref, useId, watch } from 'vue'
import { createProject } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { Project } from '../../api/types'
import TextField from '../../components/ui/TextField.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'

// The picker belongs to the settings, which are loaded only when needed.
const FolderPickerDialog = defineAsyncComponent(() => import('../settings/projects/FolderPickerDialog.vue'))

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; added: [project: Project] }>()

const session = useSession()
const announce = useAnnouncer()
const formId = useId()

const path = ref('')
const name = ref('')
const error = ref('')
const busy = ref(false)
const picking = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    path.value = ''
    name.value = ''
    error.value = ''
    picking.value = false
  },
)

function chosen(folder: string) {
  picking.value = false
  path.value = folder
  error.value = ''
}

function fail(message: string) {
  error.value = message
  announce.assertive(message)
}

async function submit() {
  if (busy.value) return
  const folder = path.value.trim()
  if (folder === '') {
    fail('Enter the path of the folder.')
    return
  }
  error.value = ''
  busy.value = true
  try {
    const { data } = await createProject(session.client, folder, name.value.trim() || undefined)
    announce.polite(`Project ${data.name} added.`)
    emit('added', data)
    emit('close')
  } catch (e) {
    fail(
      failureText(e, {
        lost: 'No answer from omo, so the project may have been added. Look in the project list before you try again.',
        refused: 'The project could not be added.',
      }),
    )
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog :open="open" title="Add project" eyebrow="projects" @close="emit('close')">
    <form :id="formId" class="grid gap-4" novalidate @submit.prevent="submit">
      <div class="add-project__path">
        <TextField
          v-model="path"
          label="Folder"
          name="path"
          autocomplete="off"
          spellcheck="false"
          placeholder="/home/you/code/app"
          hint="The full path of a git repository on this computer. Type it, or find it with Browse…"
          :error="error"
          autofocus
        />
        <UiButton class="add-project__browse" data-add-project-browse @click="picking = true">Browse…</UiButton>
      </div>
      <TextField v-model="name" label="Name (optional)" name="name" autocomplete="off" hint="Defaults to the name of the folder." />
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :form="formId" :disabled="busy">{{ busy ? 'Adding…' : 'Add project' }}</UiButton>
    </template>
  </UiDialog>
  <FolderPickerDialog
    v-if="picking"
    :open="true"
    title="Choose the project's folder"
    v-bind="path.trim() === '' ? {} : { start: path.trim() }"
    @choose="chosen"
    @close="picking = false"
  />
</template>

<style scoped>
.add-project__path {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: calc(var(--omo-space) * 2);
}
/* Level with the input below a one-line label. */
.add-project__browse {
  margin-top: calc(var(--omo-text-sm) * var(--omo-text-sm-lh) + var(--omo-space) * 1.5);
  min-height: calc(var(--omo-space) * 10);
}
</style>
