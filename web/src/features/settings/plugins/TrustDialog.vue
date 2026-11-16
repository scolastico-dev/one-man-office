<script setup lang="ts">
// The question before a plugin that is not official is installed or
// updated: the warning of its tag in plain words (the stronger one for
// community), and where the code comes from. Opened by `confirmInstall`.
import { computed } from 'vue'
import Callout from '../../../components/ui/Callout.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { TAG_WORDS, trustWarning, type TrustQuestion } from './pluginTrust'

const props = defineProps<{ question: TrustQuestion }>()
const emit = defineEmits<{ cancel: []; confirm: [] }>()

const warning = computed(() => trustWarning(props.question.tag))
const name = computed(() => visibleText(props.question.name))
const community = computed(() => props.question.tag === 'community')
</script>

<template>
  <UiDialog
    :open="true"
    :title="`${question.verb} ${name}?`"
    :eyebrow="`plugins · ${TAG_WORDS[question.tag].text}`"
    :data-trust-dialog="question.tag"
    @close="emit('cancel')"
  >
    <div class="trust">
      <Callout :tone="community ? 'danger' : 'warn'" :title="warning.title" :data-trust-warning="question.tag">
        <p v-for="(p, i) in warning.paragraphs" :key="i">{{ p }}</p>
      </Callout>
      <dl class="trust__from">
        <dt>Source</dt>
        <dd data-trust-source>{{ visibleText(question.source) }}</dd>
        <dt>Branch or tag</dt>
        <dd data-trust-ref>{{ question.ref === '' ? 'the default branch' : visibleText(question.ref) }}</dd>
        <template v-if="question.subpath !== ''">
          <dt>Folder</dt>
          <dd data-trust-folder>{{ visibleText(question.subpath) }}</dd>
        </template>
      </dl>
    </div>
    <template #actions>
      <UiButton data-trust-cancel @click="emit('cancel')">Cancel</UiButton>
      <UiButton :variant="community ? 'danger' : 'primary'" data-trust-confirm @click="emit('confirm')">{{ question.verb }} {{ name }}</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.trust {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  min-width: 0;
}
.trust p {
  margin: 0;
}
.trust__from {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 6);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.trust__from dt {
  color: var(--omo-muted);
}
.trust__from dd {
  margin: 0;
  overflow-wrap: anywhere;
}
</style>
