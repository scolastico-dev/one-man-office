<script setup lang="ts">
// "Add image…" for a Markdown text box: a file picker (several files), what
// is uploading, and the refusals of the last uploads in the server's words.
import { ref } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import { IMAGE_ACCEPT, type ImageUploads } from './imageUploads'

const props = defineProps<{ uploads: ImageUploads }>()
const input = ref<HTMLInputElement>()

function chosen() {
  const el = input.value
  if (!el?.files) return
  const files = [...el.files]
  el.value = ''
  void props.uploads.add(files)
}
</script>

<template>
  <div class="attach" data-image-attach>
    <div class="attach__row">
      <UiButton size="sm" data-add-image @click="input?.click()">Add image…</UiButton>
      <input ref="input" type="file" multiple :accept="IMAGE_ACCEPT" class="attach__input" tabindex="-1" aria-hidden="true" data-image-input @change="chosen" />
      <span class="attach__hint" role="status">{{
        uploads.busy.value > 0 ? 'Uploading…' : 'PNG, JPEG, GIF or WebP up to 10 MiB; paste or drop them into the text too.'
      }}</span>
    </div>
    <ul v-if="uploads.errors.value.length > 0" class="attach__errors" role="alert" data-image-errors>
      <li v-for="(e, i) in uploads.errors.value" :key="i">Not added: {{ e }}</li>
    </ul>
  </div>
</template>

<style scoped>
.attach {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.attach__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.attach__input {
  display: none;
}
.attach__hint {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.attach__errors {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  list-style: none;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
</style>
