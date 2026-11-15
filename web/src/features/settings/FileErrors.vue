<script setup lang="ts">
// Settings files that cannot be read, one note per file. omo keeps using the
// last version it could read; the file's path and the reader's message can
// come from anyone who can write the file, so both are shown as visible text.
import type { FileError } from '../../api/types'
import Callout from '../../components/ui/Callout.vue'
import { visibleText } from '../../components/ui/visibleText'

defineProps<{ errors: readonly FileError[] }>()

const sentence = (message: string) => visibleText(message.replace(/[.\s]+$/, ''))
</script>

<template>
  <Callout v-for="e in errors" :key="e.file" tone="danger" :title="visibleText(e.file)" data-file-error>
    <p>Cannot be read: {{ sentence(e.message) }}. omo uses the last version it could read.</p>
  </Callout>
</template>
