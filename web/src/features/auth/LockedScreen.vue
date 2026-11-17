<script setup lang="ts">
// Shown when omo refuses the board for want of a valid login token. The user
// opens the printed address again, or pastes it (or the bare token) here.
import { onMounted, ref, useId } from 'vue'
import Logo from '../../components/ui/Logo.vue'
import TextField from '../../components/ui/TextField.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { parseAccess } from '../../auth/token'

defineProps<{ reason: 'missing' | 'rejected' }>()
const emit = defineEmits<{ unlocked: [token: string] }>()

const headingId = useId()
const access = ref('')
const error = ref('')
const form = ref<HTMLFormElement>()

function submit() {
  const token = parseAccess(access.value)
  if (token === null) {
    error.value = 'That is not an omo access address or token.'
    return
  }
  error.value = ''
  access.value = ''
  emit('unlocked', token)
}

onMounted(() => form.value?.querySelector('input')?.focus())
</script>

<template>
  <main class="locked" :aria-labelledby="headingId">
    <section class="locked__box">
      <div class="locked__brand">
        <Logo :size="40" />
        <h1 :id="headingId" class="locked__title">omo is locked</h1>
      </div>
      <p class="locked__text">
        Open the address omo printed when it started. It ends with # and the login token. You can also paste that address here.
      </p>
      <p v-if="reason === 'rejected'" class="locked__text locked__text--warn" data-rejected>
        The saved login token was not accepted. It changes when someone runs omo config rotate-token.
      </p>
      <form ref="form" class="locked__form" novalidate @submit.prevent="submit">
        <TextField
          v-model="access"
          label="Access address or token"
          :error="error || undefined"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
        />
        <UiButton type="submit" variant="primary" class="locked__unlock">Unlock</UiButton>
      </form>
      <p class="locked__hint">Lost it? Run omo in a terminal: a running omo prints its address again.</p>
    </section>
  </main>
</template>

<style scoped>
.locked {
  display: grid;
  place-items: center;
  min-height: 100dvh;
  padding: calc(var(--omo-space) * 4);
}
.locked__box {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  width: min(100%, 36rem);
  padding: calc(var(--omo-space) * 6);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.locked__brand {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.locked__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  font-weight: 700;
  line-height: var(--omo-text-xl-lh);
}
.locked__text,
.locked__hint {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.locked__text--warn {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border-left: var(--omo-edge) solid var(--omo-warn);
  background: var(--omo-raised);
}
.locked__hint {
  color: var(--omo-muted);
}
.locked__form {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.locked__unlock {
  justify-self: start;
}
@media (max-width: 767.98px) {
  .locked {
    padding: calc(var(--omo-space) * 3);
  }
  .locked__box {
    padding: calc(var(--omo-space) * 4);
  }
}
</style>
