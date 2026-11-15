<script setup lang="ts">
// Shown when the board could not be loaded: omo is not running at this
// address, or answered with an error.
import Logo from '../../components/ui/Logo.vue'
import UiButton from '../../components/ui/UiButton.vue'

defineProps<{ problem: string }>()
const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <main class="unreachable" aria-labelledby="unreachable-title">
    <section class="unreachable__box">
      <div class="unreachable__brand">
        <Logo :size="40" />
        <h1 id="unreachable-title" class="unreachable__title">The board could not be loaded</h1>
      </div>
      <p class="unreachable__text" data-problem>{{ problem }}</p>
      <p class="unreachable__hint">Start omo again if it stopped, then try again.</p>
      <UiButton variant="primary" class="unreachable__retry" @click="emit('retry')">Try again</UiButton>
    </section>
  </main>
</template>

<style scoped>
.unreachable {
  display: grid;
  place-items: center;
  min-height: 100dvh;
  padding: calc(var(--omo-space) * 4);
}
.unreachable__box {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  width: min(100%, 36rem);
  padding: calc(var(--omo-space) * 6);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.unreachable__brand {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.unreachable__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  font-weight: 700;
  line-height: var(--omo-text-xl-lh);
}
.unreachable__text,
.unreachable__hint {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.unreachable__hint {
  color: var(--omo-muted);
}
.unreachable__retry {
  justify-self: start;
}
@media (max-width: 767.98px) {
  .unreachable {
    padding: calc(var(--omo-space) * 3);
  }
  .unreachable__box {
    padding: calc(var(--omo-space) * 4);
  }
}
</style>
