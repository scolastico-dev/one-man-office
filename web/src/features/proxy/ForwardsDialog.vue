<script setup lang="ts">
import UiDialog from '../../components/ui/UiDialog.vue'
import ForwardList from './ForwardList.vue'
const emit = defineEmits<{ close: [] }>()
</script>
<template>
  <UiDialog open title="Forwards" wide @close="emit('close')">
    <ForwardList @navigate="emit('close')" />
  </UiDialog>
</template>
