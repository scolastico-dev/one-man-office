<script setup lang="ts">
// "Available plugins": omo's plugin list, read from the server (which
// fetches it from the release branch of omo's repository and keeps it for a
// while). Each plugin shows its tag, description and source; Install of an
// official plugin installs at once, a recommended or community plugin asks
// first with the warning of its tag (confirmInstall) and sends the
// acknowledgement. The server decides the tag itself: when it asks for a
// stronger acknowledgement than the page knew (the list changed), the page
// asks again with the server's tag. A list that could not be read says so,
// with Retry; the rest of the page keeps working. Everything of the list is
// data from the network, shown as visible text.
import { computed, onMounted, ref } from 'vue'
import { getPluginCatalog, installPlugin } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { CatalogPlugin, PluginCatalog, PluginInfo, PluginSettings, PluginTag } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { confirmInstall } from './confirmInstall'
import { TAG_WORDS, acknowledgementOf, confirmationOf, installedFrom, usePluginDialogs, whereFrom } from './pluginTrust'

const props = defineProps<{
  /** The installed plugins' settings. */
  settings: readonly PluginSettings[]
  /** The names in use (settings and the runtime). */
  names: readonly string[]
}>()
const emit = defineEmits<{ installed: [PluginInfo] }>()

const session = useSession()
const announce = useAnnouncer()
const dialogs = usePluginDialogs()

const catalog = ref<PluginCatalog | null>(null)
const loading = ref(false)
/** The route itself failed (omo did not answer); a list omo could not read is `catalog.available` false. */
const loadError = ref('')

async function load(refresh: boolean) {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const { data } = await getPluginCatalog(session.client, refresh)
    catalog.value = data
    if (refresh) announce.polite(data.available ? "omo's plugin list is loaded." : "omo's plugin list could not be loaded.")
  } catch (e) {
    loadError.value = failureText(e, { lost: 'No answer from omo.', refused: 'omo refused to read its plugin list.' })
  } finally {
    loading.value = false
  }
}
onMounted(() => void load(false))

const unavailable = computed(() => (catalog.value !== null && !catalog.value.available ? catalog.value.message : loadError.value))
const listed = computed(() => (catalog.value?.available ? catalog.value.plugins : []))

const busy = ref<string | null>(null)
const rowError = ref<{ name: string; text: string } | null>(null)

/** Asks for `tag` (nothing for official); false when the user said no. */
async function agree(p: CatalogPlugin, tag: PluginTag): Promise<boolean> {
  const ack = acknowledgementOf(tag)
  if (ack === undefined) return true
  return confirmInstall({ name: p.name, tag: ack, source: p.source, ref: p.ref, subpath: p.subpath, verb: 'Install' }, dialogs)
}

async function install(p: CatalogPlugin) {
  if (busy.value !== null) return
  rowError.value = null
  let tag: PluginTag = p.tag
  if (!(await agree(p, tag))) return
  busy.value = p.name
  try {
    for (;;) {
      try {
        const { data } = await installPlugin(session.client, {
          name: p.name,
          source: p.source,
          subpath: p.subpath,
          ref: p.ref,
          enabled: true,
          config: {},
          acknowledge: acknowledgementOf(tag),
        })
        announce.polite(`Plugin ${visibleText(p.name)} installed.`)
        emit('installed', data)
        return
      } catch (e) {
        const asked = confirmationOf(e)
        if (asked === null || asked === tag || asked === 'official') throw e
        tag = asked
        busy.value = null
        if (!(await agree(p, tag))) return
        busy.value = p.name
      }
    }
  } catch (e) {
    const text = failureText(e, { lost: 'No answer from omo; it may have been installed. Look in the list above.', refused: 'The install was refused.' })
    rowError.value = { name: p.name, text }
    announce.assertive(visibleText(text))
  } finally {
    busy.value = null
  }
}

const installedOf = (p: CatalogPlugin) => installedFrom(p, props.settings)
const nameTaken = (p: CatalogPlugin) => props.names.includes(p.name)
</script>

<template>
  <FormSection
    legend="Available plugins"
    description="omo's plugin list. Official plugins are kept in omo's repository by its maintainers and install at once. Recommended plugins are other people's code the maintainers looked at, community plugins are not reviewed: omo asks before it installs one of those."
    data-plugin-catalog
  >
    <p v-if="catalog === null && loadError === ''" class="catalog__note" data-catalog-loading>Reading omo's plugin list…</p>
    <Callout v-else-if="unavailable !== ''" tone="warn" title="omo's plugin list could not be loaded" data-catalog-error>
      <p>{{ visibleText(unavailable) }}</p>
      <p>The installed plugins work as before, and Install plugin… installs from a source you type.</p>
      <template #actions>
        <UiButton size="sm" :disabled="loading" :aria-busy="loading ? 'true' : undefined" data-catalog-retry @click="load(true)">
          {{ loading ? 'Trying again…' : 'Retry' }}
        </UiButton>
      </template>
    </Callout>
    <p v-else-if="listed.length === 0" class="catalog__note" data-catalog-empty>omo's plugin list names no plugin.</p>
    <ul v-else class="catalog">
      <li v-for="p in listed" :key="p.name" class="catalog__row" :data-catalog-plugin="p.name" :data-tag="p.tag">
        <div class="catalog__main">
          <p class="catalog__name">
            <span>{{ visibleText(p.name) }}</span>
            <StatusBadge :tone="TAG_WORDS[p.tag].tone" :text="TAG_WORDS[p.tag].text" :title="TAG_WORDS[p.tag].meaning" data-catalog-tag />
          </p>
          <p class="catalog__desc" data-catalog-description>{{ visibleText(p.description) }}</p>
          <p class="catalog__from" data-catalog-source>{{ visibleText(whereFrom(p)) }}</p>
          <Callout v-if="rowError?.name === p.name" tone="danger" data-catalog-install-error>
            <p>{{ visibleText(rowError.text) }}</p>
          </Callout>
        </div>
        <p v-if="installedOf(p)" class="catalog__meta" data-catalog-installed>Installed</p>
        <p v-else-if="nameTaken(p)" class="catalog__meta" data-catalog-name-taken>Another plugin has this name</p>
        <UiButton
          v-else
          size="sm"
          :disabled="busy !== null"
          :aria-busy="busy === p.name ? 'true' : undefined"
          data-catalog-install
          @click="install(p)"
        >
          {{ busy === p.name ? 'Installing…' : 'Install' }}<span class="sr-only">{{ ' ' + visibleText(p.name) }}</span>
        </UiButton>
      </li>
    </ul>
  </FormSection>
</template>

<style scoped>
.catalog__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.catalog {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.catalog__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.catalog__row > :deep(button) {
  flex-shrink: 0;
}
.catalog__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.catalog__main p {
  margin: 0;
  overflow-wrap: anywhere;
}
.catalog__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.catalog__desc {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.catalog__from {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.catalog__meta {
  flex-shrink: 0;
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
