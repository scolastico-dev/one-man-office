<script setup lang="ts">
// The fields a prompt template can use, one line each. The list is checked
// against the fields of the default prompts and of omo's template data.
import { TEMPLATE_FIELDS, PROJECT_FIELDS } from './templateFields'

</script>

<template>
  <details class="template-fields" data-template-fields>
    <summary>Template fields</summary>
    <p class="template-fields__lead">
      Write a field as <code v-pre>{{.Name}}</code>. An unknown field is refused when you save. The preview fills the fields with sample data.
    </p>
    <dl class="template-fields__list">
      <template v-for="f in TEMPLATE_FIELDS" :key="f.name">
        <dt><code>{{ f.name }}</code></dt>
        <dd>{{ f.text }}</dd>
      </template>
    </dl>
    <p class="template-fields__lead">Inside <code v-pre>{{range .Projects}}</code> each project has:</p>
    <dl class="template-fields__list">
      <template v-for="f in PROJECT_FIELDS" :key="f.name">
        <dt><code>{{ f.name }}</code></dt>
        <dd>{{ f.text }}</dd>
      </template>
    </dl>
  </details>
</template>

<style scoped>
.template-fields {
  border: var(--omo-border) solid var(--omo-line);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.template-fields > summary {
  cursor: pointer;
  min-height: calc(var(--omo-space) * 6);
  display: flex;
  align-items: center;
}
.template-fields__lead {
  margin: calc(var(--omo-space) * 2) 0;
  color: var(--omo-muted);
}
.template-fields__list {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--omo-space) calc(var(--omo-space) * 4);
  margin: 0;
}
.template-fields__list dt,
.template-fields__list dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (max-width: 480px) {
  .template-fields__list {
    grid-template-columns: 1fr;
    gap: 0;
  }
  .template-fields__list dd {
    margin-bottom: calc(var(--omo-space) * 2);
    color: var(--omo-muted);
  }
}
</style>
