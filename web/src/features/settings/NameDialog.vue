<script setup lang="ts">
// Names a new item of a settings page (a usage script, a skill set). Nothing
// is stored here: the item's editor opens, and the item is stored when the
// user saves it. The server decides which names are allowed and says so
// when the editor is saved; the dialog only refuses an empty name and one
// the list already has. Its words come from the page.
import { ref, useId, watch } from 'vue'
import TextField from '../../components/ui/TextField.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { visibleText } from '../../components/ui/visibleText'

const props = withDefaults(
  defineProps<{
    open: boolean
    /** The names in use. */
    taken?: readonly string[]
    /** The dialog's title, e.g. "New script". */
    title: string
    /** The small line above the title, e.g. "usage". */
    eyebrow: string
    /** What is named, e.g. "script": in the field's label and the refusal of a name in use. */
    thing: string
    /** The button that creates it, e.g. "Create". */
    confirm: string
    /** The rule of the name in the field's hint, when it is not the usual one. */
    rule?: string
  }>(),
  { taken: () => [], rule: 'Lowercase letters, digits, dots, dashes and underscores.' },
)
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
  if (props.taken.includes(n)) return fail(`A ${props.thing} ${visibleText(n)} exists already.`)
  emit('create', n)
  emit('close')
}
</script>

<template>
  <UiDialog :open="open" :title="title" :eyebrow="eyebrow" data-name-dialog @close="emit('close')">
    <form :id="formId" class="grid gap-4" novalidate data-name-form @submit.prevent="submit">
      <TextField
        v-model="name"
        :label="`Name of the ${thing}`"
        name="name"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        :hint="`${rule} It is fixed once saved.`"
        :error="error"
        autofocus
      />
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :form="formId" data-primary>{{ confirm }}</UiButton>
    </template>
  </UiDialog>
</template>
