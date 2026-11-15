<script setup lang="ts">
// The card that follows the pointer while it is dragged. Its position is set
// by the drag itself, outside Vue, so moving the pointer renders nothing.
import { onBeforeUnmount, onMounted, ref } from 'vue'
import TicketCard from './TicketCard.vue'
import type { CardModel } from './model'

const props = defineProps<{
  card: CardModel
  width: number
  /** Receives the element to move; null when it goes. */
  place: (el: HTMLElement | null) => void
}>()

const el = ref<HTMLElement>()
onMounted(() => props.place(el.value ?? null))
onBeforeUnmount(() => props.place(null))
</script>

<template>
  <Teleport to="body">
    <div ref="el" class="ghost" aria-hidden="true" inert data-drag-ghost :style="{ width: `${width}px` }">
      <ul role="presentation" class="ghost__list">
        <TicketCard :card="card" ghost />
      </ul>
    </div>
  </Teleport>
</template>

<style scoped>
.ghost {
  position: fixed;
  z-index: 50;
  top: 0;
  left: 0;
  pointer-events: none;
  will-change: transform;
}
.ghost__list {
  margin: 0;
  padding: 0;
  list-style: none;
}
.ghost :deep(.card) {
  border-color: var(--omo-accent);
  cursor: grabbing;
}
</style>
