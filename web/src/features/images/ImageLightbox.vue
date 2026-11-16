<script setup lang="ts">
// An uploaded image at full size, in a dialog. The picture comes from the
// object URL the view already holds; its alt text (written by people or
// agents) is the dialog's title and the picture's name, shown as text.
import { computed } from 'vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'

export interface LightboxImage {
  url: string
  alt: string
}

const props = defineProps<{ image: LightboxImage | null }>()
const emit = defineEmits<{ close: [] }>()

const title = computed(() => (props.image ? visibleText(props.image.alt) || 'Image' : 'Image'))
</script>

<template>
  <UiDialog :open="image !== null" :title="title" eyebrow="Image" wide data-image-lightbox @close="emit('close')">
    <div v-if="image" class="lightbox">
      <img :src="image.url" :alt="title" class="lightbox__img" />
    </div>
  </UiDialog>
</template>

<style scoped>
.lightbox {
  overflow: auto;
  max-height: 75dvh;
}
.lightbox__img {
  display: block;
  max-width: 100%;
  margin: 0 auto;
}
</style>
