<script setup lang="ts">
// Names a new skill set. Nothing is stored here: the set's form opens, filled
// in or not, and is stored when the user saves it. The server decides which
// names are allowed and says so when the form is saved.
import { ref, useId, watch } from 'vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'

const props = withDefaults(defineProps<{ open: boolean; taken?: readonly string[] }>(), { taken: () => [] })
const emit = defineEmits<{ close: []; create: [name: string] }>()

const announce = useAnnouncer()
const formId = useId()
const name = ref('')
const error = ref('')

watch(
  () => props.open,
  (open) => {
    if (!open) return
    name.value = ''
    error.value = ''
  },
  { immediate: true },
)

function submit() {
  const n = name.value.trim()
  const fail = (m: string) => {
    error.value = m
    announce.assertive(m)
  }
  if (n === '') return fail('Enter a name.')
  if (props.taken.includes(n)) return fail(`A skill set ${visibleText(n)} exists already.`)
  emit('create', n)
  emit('close')
}
</script>

<template>
  <UiDialog :open="open" title="Add skill set" eyebrow="skill sets" @close="emit('close')">
    <form :id="formId" class="grid gap-4" novalidate data-skillset-name-form @submit.prevent="submit">
      <TextField
        v-model="name"
        label="Name of the skill set"
        name="name"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        hint="Lowercase letters, digits, dots, dashes and underscores. It is fixed once saved."
        :error="error"
        autofocus
      />
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :form="formId" data-primary>Add</UiButton>
    </template>
  </UiDialog>
</template>
