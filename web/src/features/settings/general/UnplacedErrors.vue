<script setup lang="ts">
// Refusals of a save that name a key the page shows no field for (the server
// names the group when a value of it cannot be read at all). The server's
// sentence is shown as it is, above the form.
import Callout from '../../../components/ui/Callout.vue'
import { visibleText } from '../../../components/ui/visibleText'

defineProps<{ errors: readonly string[] }>()
</script>

<template>
  <Callout v-if="errors.length > 0" tone="danger" live="assertive" data-form-errors>
    <p v-for="e in errors" :key="e">{{ visibleText(e) }}</p>
  </Callout>
</template>
