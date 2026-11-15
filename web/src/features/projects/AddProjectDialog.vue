<script setup lang="ts">
// Adds a project by the path of its folder. The server decides whether the
// folder is a usable git repository; its answer is shown next to the field.
import { ref, useId, watch } from 'vue'
import { ApiError } from '../../api/client'
import { createProject } from '../../api/endpoints'
import type { Project } from '../../api/types'
import TextField from '../../components/ui/TextField.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; added: [project: Project] }>()

const session = useSession()
const announce = useAnnouncer()
const formId = useId()

const path = ref('')
const name = ref('')
const error = ref('')
const busy = ref(false)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    path.value = ''
    name.value = ''
    error.value = ''
  },
)

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
    fail(e instanceof ApiError ? e.message : 'The project could not be added.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog :open="open" title="Add project" eyebrow="projects" @close="emit('close')">
    <form :id="formId" class="grid gap-4" novalidate @submit.prevent="submit">
      <TextField
        v-model="path"
        label="Folder"
        name="path"
        autocomplete="off"
        spellcheck="false"
        placeholder="/home/you/code/app"
        hint="Type or paste the full path of a git repository on this computer, as omo has no folder picker."
        :error="error"
        autofocus
      />
      <TextField v-model="name" label="Name (optional)" name="name" autocomplete="off" hint="Defaults to the name of the folder." />
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :form="formId" :disabled="busy">{{ busy ? 'Adding…' : 'Add project' }}</UiButton>
    </template>
  </UiDialog>
</template>
