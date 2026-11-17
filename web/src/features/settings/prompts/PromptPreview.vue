<script setup lang="ts">
// The draft of a prompt as an agent would get it, rendered with sample data by
// the server. It is asked for while the draft changes (debounced; an older
// answer never replaces a newer one). The text is plain text, never rendered
// as Markdown or HTML, and scrolls inside its own box.
import { onMounted, useId, watch } from 'vue'
import { previewPrompt } from '../../../api/endpoints'
import Callout from '../../../components/ui/Callout.vue'
import { visibleLines, visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { usePromptPreview } from './usePromptPreview'

const props = defineProps<{ name: string; text: string }>()

const { client } = useSession()
const labelId = useId()
const preview = usePromptPreview({ request: async (name, text) => (await previewPrompt(client, name, text)).data })

onMounted(() => preview.now(props.name, props.text))
watch(
  () => [props.name, props.text] as const,
  ([name, text]) => preview.schedule(name, text),
)
</script>

<template>
  <div class="prompt-preview" data-prompt-preview :aria-busy="preview.busy.value ? 'true' : undefined">
    <p :id="labelId" class="prompt-preview__label">
      Preview with sample data<span v-if="preview.busy.value" class="prompt-preview__busy"> (updating…)</span>
    </p>
    <div class="prompt-preview__body">
      <Callout v-if="preview.failure.value" tone="danger" title="Preview" data-preview-failure>
        <p>{{ visibleText(preview.failure.value) }}</p>
      </Callout>
      <Callout v-else-if="preview.error.value" tone="danger" title="Template" data-preview-error>
        <p>{{ visibleText(preview.error.value) }}</p>
      </Callout>
      <pre v-else-if="preview.ready.value" class="prompt-preview__text" role="region" tabindex="0" :aria-labelledby="labelId" data-preview-text>{{ visibleLines(preview.text.value) }}</pre>
      <p v-else class="prompt-preview__wait">Rendering…</p>
    </div>
  </div>
</template>

<style scoped>
.prompt-preview {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-bg);
}
.prompt-preview__label {
  margin: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 3);
  border-bottom: var(--omo-border) solid var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.prompt-preview__body {
  min-width: 0;
  padding: 0;
}
.prompt-preview__body > :deep(.callout) {
  margin: calc(var(--omo-space) * 2);
}
.prompt-preview__text {
  box-sizing: border-box;
  max-height: calc(var(--omo-space) * 90);
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  overflow: auto;
  font-family: inherit;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.prompt-preview__wait {
  margin: 0;
  padding: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
