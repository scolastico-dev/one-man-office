<script setup lang="ts">
// The pause switch: shows and sets whether omo starts new agents
// (`setPaused`); while paused the top bar says so. For now it shows the
// server's state and changes nothing.
import { computed } from 'vue'
import Toggle from '../../components/ui/Toggle.vue'
import { useBoard } from '../../store'

const store = useBoard()
const paused = computed({
  get: () => store.state.paused,
  set: () => {},
})
</script>

<template>
  <Toggle v-model="paused" label="Pause" />
</template>
