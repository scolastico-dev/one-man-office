<script setup lang="ts">
// The ticket's place over the board, at `/<n>`: header with type, number,
// editable title, column, badge and actions; the description; the fields
// (slot `fields`) and, later, tabs (slot `tabs`). For now it shows the title
// only, in a dialog that Escape and the close button close.
import { computed } from 'vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useBoard } from '../../store'

const props = defineProps<{ number: number }>()
const emit = defineEmits<{ close: [] }>()

const store = useBoard()
const ticket = computed(() => store.view(props.number))
</script>

<template>
  <UiDialog open wide :eyebrow="`#${number}`" :title="ticket?.title ?? `Ticket #${number}`" data-ticket-panel @close="emit('close')" />
</template>
