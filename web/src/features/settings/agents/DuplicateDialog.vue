<script setup lang="ts">
// Names a profile: a copy of a stored one (Duplicate…) or a new one (New
// profile). A profile's name is part of every model id roles and tickets
// store, so it is fixed once saved; a copy under a new name is how a profile
// gets another one. The copy is written at once with If-Match "0", so it
// never replaces a profile of that name. A hidden value belongs to the
// profile it is stored in: the copy leaves those variables out and says so.
// A new profile is only named here; the form saves it.
import { computed, ref, useId, watch } from 'vue'
import { NO_REVISION, type Profile } from '../../../api/types'
import { putProfile } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import SelectField from '../../../components/ui/SelectField.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { CLI_OPTIONS, duplicateProfile } from './profileForm'

const props = withDefaults(
  defineProps<{
    open: boolean
    /** The stored profile to copy; null names a new profile. */
    source?: Profile | null
    /** Names of the profiles that exist, so a new one does not reuse a name. */
    taken?: readonly string[]
  }>(),
  { source: null, taken: () => [] },
)
const emit = defineEmits<{
  close: []
  /** The copy was written. */
  duplicated: [name: string]
  /** A new profile was named; nothing is stored yet. */
  create: [name: string, cli: string]
}>()

const session = useSession()
const announce = useAnnouncer()
const formId = useId()

const name = ref('')
const cli = ref('claude')
const error = ref('')
const busy = ref(false)

const copying = computed(() => props.source !== null)
const dropped = computed(() => (props.source === null ? [] : duplicateProfile(props.source, 'x').dropped))
const title = computed(() => (props.source === null ? 'New profile' : `Duplicate ${visibleText(props.source.name)}`))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    name.value = props.source === null ? '' : `${props.source.name}-copy`
    cli.value = 'claude'
    error.value = ''
  },
  { immediate: true },
)

function fail(message: string) {
  error.value = message
  announce.assertive(message)
}

async function submit() {
  if (busy.value) return
  const n = name.value.trim()
  if (n === '') return fail('Enter a name.')
  if (props.taken.includes(n)) return fail(`A profile ${visibleText(n)} exists already.`)
  if (props.source === null) {
    emit('create', n, cli.value)
    emit('close')
    return
  }
  error.value = ''
  busy.value = true
  try {
    await putProfile(session.client, duplicateProfile(props.source, n).profile, NO_REVISION)
    announce.polite(`Profile ${visibleText(n)} created.`)
    emit('duplicated', n)
    emit('close')
  } catch (e) {
    fail(
      failureText(e, {
        lost: 'No answer from omo, so the copy may have been written. Look in the profile list before you try again.',
        refused: 'The copy could not be written.',
      }),
    )
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog :open="open" :title="title" eyebrow="agents" @close="emit('close')">
    <form :id="formId" class="grid gap-4" novalidate data-profile-name-form @submit.prevent="submit">
      <TextField
        v-model="name"
        label="Name of the profile"
        name="name"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        hint="Lowercase letters, digits, dots, dashes and underscores. Models are named <profile>/<model>, so it is fixed once saved."
        :error="error"
        autofocus
      />
      <SelectField v-if="!copying" v-model="cli" label="Agent program" :options="[...CLI_OPTIONS]" />
      <p v-if="dropped.length" class="dup__note" data-dropped>
        Hidden values are not copied. Enter them again in the copy: {{ dropped.map((d) => visibleText(d)).join(', ') }}.
      </p>
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :form="formId" :disabled="busy" data-primary>
        {{ copying ? (busy ? 'Copying…' : 'Duplicate') : 'Create' }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.dup__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
