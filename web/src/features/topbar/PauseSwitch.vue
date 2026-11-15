<script setup lang="ts">
// The pause switch: shows the server's state and sets it (`setPaused`). A
// click shows the wanted state only until the server's event confirms it, and
// never longer than the pending period; a refusal shows the server's answer.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { setPaused } from '../../api/endpoints'
import Toggle from '../../components/ui/Toggle.vue'
import { useToast } from '../../components/ui/useToast'
import { useSession } from '../../session'
import { useBoard } from '../../store'

/** How long a wanted state is shown without the server's confirmation. */
const PENDING_MS = 5000

const store = useBoard()
const { client } = useSession()
const toast = useToast()

/** The state the user asked for while the server has not confirmed it; null otherwise. */
const wanted = ref<boolean | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined
function settle() {
  clearTimeout(timer)
  wanted.value = null
}
watch(
  () => store.state.paused,
  (paused) => {
    if (wanted.value === paused) settle()
  },
)
onBeforeUnmount(() => clearTimeout(timer))

const shown = computed({
  get: () => wanted.value ?? store.state.paused,
  set(value: boolean) {
    if (wanted.value !== null || value === store.state.paused) return
    wanted.value = value
    clearTimeout(timer)
    timer = setTimeout(settle, PENDING_MS)
    setPaused(client, value).catch((e: unknown) => {
      settle()
      toast.show({
        tone: 'error',
        title: value ? 'Could not pause omo' : 'Could not resume omo',
        message: e instanceof Error ? e.message : undefined,
      })
    })
  },
})
</script>

<template>
  <Toggle v-model="shown" label="Paused" :disabled="wanted !== null" :aria-busy="wanted !== null ? 'true' : undefined" />
</template>
