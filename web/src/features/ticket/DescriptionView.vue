<script setup lang="ts">
// A description rendered from Markdown (see markdown.ts for what is let
// through). A `#12` link opens that ticket in place; other links open a new tab.
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ticketHere } from '../../locations'
import { useBoard } from '../../store'
import { renderMarkdown } from './markdown'

const props = defineProps<{ text: string }>()

const store = useBoard()
const route = useRoute()
const router = useRouter()

const html = computed(() =>
  renderMarkdown(props.text, (n) => {
    const t = store.view(n)
    return t === undefined ? undefined : { title: t.title, column: t.column }
  }),
)

function onClick(e: MouseEvent) {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const a = (e.target as Element | null)?.closest?.('a[data-ticket-ref]')
  if (!a) return
  e.preventDefault()
  void router.push(ticketHere(Number(a.getAttribute('data-ticket-ref')), route))
}
</script>

<template>
  <!-- markdown.ts renders with raw HTML off and every link checked. -->
  <div class="md" data-description v-html="html" @click="onClick" />
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
.md :deep(h4),
.md :deep(h5),
.md :deep(h6) {
  margin: calc(var(--omo-space) * 5) 0 calc(var(--omo-space) * 2);
  line-height: var(--omo-text-lg-lh);
}
.md :deep(h4) {
  font-size: var(--omo-text-lg);
}
.md :deep(h5) {
  color: var(--omo-accent);
  font-size: var(--omo-text-base);
}
.md :deep(h6) {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
}
.md :deep(ul),
.md :deep(ol) {
  padding-left: calc(var(--omo-space) * 6);
}
.md :deep(ul) {
  list-style: square;
}
.md :deep(ol) {
  list-style: decimal;
}
.md :deep(a) {
  color: var(--omo-accent);
}
.md :deep(a[data-ticket-ref]) {
  text-decoration-style: dotted;
}
.md :deep(code) {
  padding: 0 var(--omo-space);
  background: var(--omo-raised);
  font-size: var(--omo-text-sm);
}
.md :deep(pre) {
  padding: calc(var(--omo-space) * 3);
  overflow-x: auto;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
}
.md :deep(pre code) {
  padding: 0;
  background: transparent;
  overflow-wrap: normal;
}
.md :deep(blockquote) {
  padding-left: calc(var(--omo-space) * 3);
  border-left: var(--omo-edge) solid var(--omo-line-strong);
  color: var(--omo-muted);
}
.md :deep(table) {
  display: block;
  max-width: 100%;
  overflow-x: auto;
  border-collapse: collapse;
  font-size: var(--omo-text-sm);
}
.md :deep(th),
.md :deep(td) {
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-line);
  text-align: left;
}
.md :deep(hr) {
  border: 0;
  border-top: var(--omo-border) solid var(--omo-line);
}
</style>
