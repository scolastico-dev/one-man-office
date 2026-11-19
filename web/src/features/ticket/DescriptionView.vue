<script setup lang="ts">
// A description rendered from Markdown (see markdown.ts for what is let
// through). A `#12` link opens that ticket in place; other links open a new
// tab. Uploaded images show through object URLs (inlineImages.ts) and open
// full size in a dialog.
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useBoard } from '../../store'
import { openTicketLocation } from './openTicket'
import ImageLightbox, { type LightboxImage } from '../images/ImageLightbox.vue'
import { lightboxFor } from '../images/lightbox'
import { useInlineImages } from '../images/inlineImages'
import { renderMarkdown } from './markdown'

const props = defineProps<{ text: string }>()

const store = useBoard()
const route = useRoute()
const router = useRouter()
const images = useInlineImages(() => props.text)
const lightbox = ref<LightboxImage | null>(null)

const html = computed(() =>
  renderMarkdown(
    props.text,
    (n) => {
      const t = store.view(n)
      return t === undefined ? undefined : { title: t.title, column: t.column }
    },
    { images: images.view },
  ),
)

function onClick(e: MouseEvent) {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const shown = lightboxFor(e.target, images)
  if (shown) {
    lightbox.value = shown
    return
  }
  const a = (e.target as Element | null)?.closest?.('a[data-ticket-ref]')
  if (!a) return
  e.preventDefault()
  void router.push(openTicketLocation(store, Number(a.getAttribute('data-ticket-ref')), route))
}
</script>

<template>
  <!-- markdown.ts renders with raw HTML off and every link checked. -->
  <div class="md" data-description v-html="html" @click="onClick" />
  <ImageLightbox v-if="lightbox" :image="lightbox" @close="lightbox = null" />
</template>

<style scoped>
.md {
  min-width: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
  overflow-wrap: anywhere;
}
.md :deep(> :first-child) {
  margin-top: 0;
}
.md :deep(> :last-child) {
  margin-bottom: 0;
}
.md :deep(p),
.md :deep(ul),
.md :deep(ol),
.md :deep(blockquote),
.md :deep(pre),
.md :deep(table) {
  margin: 0 0 calc(var(--omo-space) * 3);
}
.md :deep(a) {
  color: var(--omo-accent);
}
.md :deep(code) {
  padding: 0 var(--omo-space);
  background: var(--omo-raised);
  font-size: var(--omo-text-sm);
}
.md :deep(pre) {
  padding: calc(var(--omo-space) * 3);
  overflow-x: auto;
  /* A composited layer: the compositor then finds this scroll box under a wheel itself, where asking the main thread can drop the wheel (the panel's code blocks needed it). */
  will-change: transform;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
}
.md :deep(pre code) {
  padding: 0;
  background: transparent;
  overflow-wrap: normal;
}
</style>
