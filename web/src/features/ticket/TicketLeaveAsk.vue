<script setup lang="ts">
// Asked in the ticket's head when leaving it would lose unsaved text.
import { ref } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'

defineProps<{ id: string }>()
const emit = defineEmits<{ keep: []; discard: [] }>()

const keepButton = ref<InstanceType<typeof UiButton>>()
defineExpose({ focus: () => (keepButton.value?.$el as HTMLElement | undefined)?.focus() })
</script>

<template>
  <div class="tleave" role="alertdialog" :aria-labelledby="id" data-unsaved>
    <p :id="id" class="tleave__text">You have unsaved changes. Leave without saving them?</p>
    <div class="tleave__row">
      <UiButton ref="keepButton" size="sm" variant="primary" data-keep-editing @click="emit('keep')">Keep editing</UiButton>
      <UiButton size="sm" variant="danger" data-discard @click="emit('discard')">Discard changes</UiButton>
    </div>
  </div>
</template>

<style scoped>
.tleave {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.tleave__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.tleave__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
</style>
