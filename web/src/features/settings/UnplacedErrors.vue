<script setup lang="ts">
// The one place a settings form shows a refusal that names a key it has no
// field for (the server names a group when it holds a key omo does not
// know, or a key the page does not edit). Every page follows one rule: a
// refusal goes under the field it names when the form shows that field,
// otherwise here, above the form, in the server's words; a refusal naming no
// field at all is shown in the Save bar.
import Callout from '../../components/ui/Callout.vue'
import { visibleText } from '../../components/ui/visibleText'

defineProps<{ errors: readonly string[] }>()
</script>

<template>
  <Callout v-if="errors.length > 0" tone="danger" live="assertive" data-form-errors>
    <p v-for="e in errors" :key="e">{{ visibleText(e) }}</p>
  </Callout>
</template>
