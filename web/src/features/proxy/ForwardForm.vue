<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { fieldOf } from '../../api/client'
import { createProxyForward } from '../../api/endpoints'
import type { ProxyForward, TicketSummary } from '../../api/types'
import Callout from '../../components/ui/Callout.vue'
import NumberField from '../../components/ui/NumberField.vue'
import SelectField from '../../components/ui/SelectField.vue'
import TextField from '../../components/ui/TextField.vue'
import Toggle from '../../components/ui/Toggle.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { useBoard } from '../../store'

const props = defineProps<{ ticket?: TicketSummary }>()
const emit = defineEmits<{ added: [forward: ProxyForward]; cancel: [] }>()
const { client } = useSession()
const store = useBoard()
const scope = ref('global')
const ticketNumber = ref<number | null>(null)
const port = ref<number | null>(null)
const portParse = ref<string | null>(null)
const ticketParse = ref<string | null>(null)
const fields = reactive({ name: '', host: '127.0.0.1', scheme: 'http', start_path: '/', strip_prefix: false, verify_tls: false })
const busy = ref(false)
const problem = ref('')
const errors = ref<Record<string, string>>({})
const owner = computed(() => props.ticket?.number ?? (scope.value === 'ticket' ? ticketNumber.value : null))

async function save() {
  if (busy.value) return
  problem.value = ''
  errors.value = {}
  if (portParse.value || port.value === null || !Number.isInteger(port.value) || port.value < 1 || port.value > 65535) {
    errors.value.port = 'Enter a port from 1 to 65535.'
  }
  if (!props.ticket && scope.value === 'ticket' && (ticketParse.value || owner.value === null || !Number.isSafeInteger(owner.value) || owner.value < 1)) {
    errors.value.ticket = 'Enter a ticket number.'
  }
  if (!fields.name.trim()) errors.value.name = 'Enter a name for the forward.'
  const t = props.ticket ?? (owner.value === null ? undefined : store.view(owner.value))
  if (t?.column === 'done') errors.value.ticket = 'This ticket is done; add a global forward instead.'
  const first = Object.values(errors.value)[0]
  if (first) {
    problem.value = first
    return
  }
  busy.value = true
  try {
    const { data } = await createProxyForward(client, {
      ...fields,
      host: fields.host.trim() || '127.0.0.1',
      start_path: fields.start_path || '/',
      ticket: owner.value,
      port: port.value!,
    })
    emit('added', data)
  } catch (e) {
    problem.value = visibleText(e instanceof Error ? e.message : 'Could not add the forward.')
    const field = fieldOf(e)
    if (field) errors.value[field] = problem.value
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="forward-form" data-forward-form novalidate @submit.prevent="save">
    <template v-if="!ticket">
      <SelectField v-model="scope" name="scope" label="Belongs to" :options="[{ value: 'global', label: 'Global' }, { value: 'ticket', label: 'Ticket' }]" :disabled="busy" />
      <NumberField v-if="scope === 'ticket'" v-model="ticketNumber" name="ticket" label="Ticket number" :min="1" required :error="errors.ticket" :disabled="busy" @parse="ticketParse = $event" />
    </template>
    <NumberField v-model="port" name="port" label="Port (required)" :min="1" :max="65535" required :error="errors.port" :disabled="busy" @parse="portParse = $event" />
    <TextField v-model="fields.name" name="name" label="Name (required)" required :error="errors.name" :disabled="busy" />
    <TextField v-model="fields.host" name="host" label="Host" hint="Defaults to 127.0.0.1 on the omo host." :error="errors.host" :disabled="busy" />
    <SelectField v-model="fields.scheme" name="scheme" label="Scheme" :options="[{ value: 'http', label: 'http' }, { value: 'https', label: 'https' }]" :error="errors.scheme" :disabled="busy" />
    <TextField v-model="fields.start_path" name="start_path" label="Start path" hint="Starts with /; may include a query." :error="errors.start_path" :disabled="busy" />
    <div class="forward-form__option">
      <Toggle v-model="fields.strip_prefix" label="Strip prefix" :disabled="busy" />
      <p class="omo-field__hint">For path mode, configure the app with the base path shown after adding. Strip the prefix only for apps that use relative addresses.</p>
    </div>
    <div class="forward-form__option">
      <Toggle v-model="fields.verify_tls" label="Verify TLS certificate" :disabled="busy" />
      <p class="omo-field__hint">For HTTPS targets. Off accepts self-signed certificates; on requires a trusted certificate.</p>
    </div>
    <Callout v-if="problem" tone="danger" live="assertive">{{ problem }}</Callout>
    <div class="forward-form__actions">
      <UiButton :disabled="busy" @click="emit('cancel')">Cancel</UiButton>
      <UiButton type="submit" variant="primary" :disabled="busy">{{ busy ? 'Adding…' : 'Add forward' }}</UiButton>
    </div>
  </form>
</template>

<style scoped>
.forward-form {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  padding: calc(var(--omo-space) * 4);
  border: var(--omo-border) solid var(--omo-line);
}
.forward-form__option {
  display: grid;
  gap: var(--omo-space);
}
.forward-form__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2);
}
</style>
