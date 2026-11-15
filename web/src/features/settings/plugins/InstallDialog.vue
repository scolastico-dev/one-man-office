<script setup lang="ts">
// Installs a plugin from a git address or a local folder. Before the user
// confirms, the dialog says plainly what installing means: the plugin is
// code that runs on this computer as the user. The address is shown as
// typed (as visible text). Installing fetches and checks the plugin, which
// can take a while: the button says so and a second press sends nothing.
// The server's refusal is shown in its own words: under the field it names
// (name, source, subpath, ref), anything else above the fields. While omo
// installs, the dialog stays open (Cancel is disabled, closing it says why),
// so the answer is always seen here.
import { computed, ref, useId, watch } from 'vue'
import { ApiError, fieldOf } from '../../../api/client'
import { installPlugin } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { PluginInfo } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { INSTALL_FIELDS, suggestName } from './pluginForms'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; installed: [PluginInfo] }>()

const session = useSession()
const announce = useAnnouncer()
const formId = useId()

const source = ref('')
const subpath = ref('')
const refName = ref('')
const name = ref('')
/** The user typed a name: it no longer follows the source. */
const namedByHand = ref(false)
const busy = ref(false)
const fieldErrors = ref<Record<string, string>>({})
const error = ref('')
/** Closing was asked for while omo installs. */
const closeRefused = ref(false)
const BUSY_NOTE = 'omo is still installing the plugin; the dialog closes when omo has answered.'

function requestClose() {
  if (busy.value) {
    closeRefused.value = true
    announce.assertive(BUSY_NOTE)
    return
  }
  emit('close')
}

watch(
  () => props.open,
  (open) => {
    if (!open || busy.value) return
    source.value = ''
    subpath.value = ''
    refName.value = ''
    name.value = ''
    namedByHand.value = false
    fieldErrors.value = {}
    error.value = ''
  },
  { immediate: true },
)

watch(source, (s) => {
  if (!namedByHand.value) name.value = suggestName(s)
})
function onName(v: string) {
  name.value = v
  namedByHand.value = v.trim() !== ''
}

const typed = computed(() => source.value.trim())

async function submit() {
  if (busy.value) return
  fieldErrors.value = {}
  error.value = ''
  busy.value = true
  const n = name.value.trim()
  try {
    const { data } = await installPlugin(session.client, { name: n, source: source.value.trim(), subpath: subpath.value.trim(), ref: refName.value.trim(), enabled: true, config: {} })
    announce.polite(`Plugin ${visibleText(n)} installed.`)
    emit('installed', data)
    emit('close')
  } catch (e) {
    const field = fieldOf(e)
    const text = failureText(e, { lost: 'No answer from omo; the install may still be running. The list shows what omo installed.', refused: 'The install was refused.' })
    if (field !== null && INSTALL_FIELDS.includes(field)) fieldErrors.value = { [field]: text }
    else error.value = text
    announce.assertive(visibleText(text))
    if (!(e instanceof ApiError) || field === null) return
    // Focus the field the server named.
    document.querySelector<HTMLElement>(`[data-install-form="${formId}"] [name="${field}"]`)?.focus()
  } finally {
    busy.value = false
    closeRefused.value = false
  }
}

const fe = (f: string) => (fieldErrors.value[f] ? visibleText(fieldErrors.value[f]!) : undefined)
</script>

<template>
  <UiDialog :open="open" title="Install a plugin" eyebrow="plugins" data-install-dialog @close="requestClose">
    <form :id="formId" class="install" :data-install-form="formId" novalidate @submit.prevent="submit">
      <Callout tone="warn" title="A plugin runs as you" data-install-warning>
        <p>
          A plugin is code that omo runs on this computer as you, with your rights: it can read and change your files and use your accounts. Install only
          plugins you trust.
        </p>
        <p v-if="typed !== ''" class="install__from">
          omo installs the code from <code data-install-address>{{ visibleText(typed) }}</code>
        </p>
      </Callout>
      <Callout v-if="closeRefused" tone="warn" data-install-busy>
        <p>{{ BUSY_NOTE }}</p>
      </Callout>
      <Callout v-if="error" tone="danger" data-install-error>
        <p>{{ visibleText(error) }}</p>
      </Callout>
      <TextField
        v-model="source"
        label="Source"
        name="source"
        hint="A git address (https or ssh) or a folder on this computer."
        :error="fe('source')"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        autofocus
        data-install-source
      />
      <TextField
        v-model="subpath"
        label="Folder in the repository"
        name="subpath"
        hint="Empty: the plugin is at the top of the repository."
        :error="fe('subpath')"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
      />
      <TextField
        v-model="refName"
        label="Branch or tag"
        name="ref"
        hint="Empty: the default branch."
        :error="fe('ref')"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
      />
      <TextField
        :model-value="name"
        label="Name"
        name="name"
        hint="Letters, digits, dots, dashes and underscores. It is fixed once installed."
        :error="fe('name')"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        data-install-name
        @update:model-value="onName"
      />
    </form>
    <template #actions>
      <UiButton :disabled="busy" :title="busy ? BUSY_NOTE : undefined" data-install-cancel @click="requestClose">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :form="formId" :disabled="busy" :aria-busy="busy ? 'true' : undefined" data-primary data-install-submit>
        {{ busy ? 'Installing…' : 'Install' }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.install {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
}
.install__from {
  margin-top: var(--omo-space);
  overflow-wrap: anywhere;
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
