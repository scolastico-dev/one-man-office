<script setup lang="ts">
// Searching skills.sh for a skill to add as a skill set. Typing searches
// after a short pause, through omo's server (only the search text reaches
// skills.sh); answers are numbered so an older one never replaces a newer
// one. Every text of a result is someone else's and is shown as plain text.
// "Use" asks omo to look at the result's repository for the skill's folder
// and hands the result on; nothing is stored here.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { resolveSkillSearch, searchSkills } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { SkillSearchResolved, SkillSearchResult } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import TextField from '../../../components/ui/TextField.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { installWords, MAX_SEARCH, MIN_SEARCH, SEARCH_DELAY, searchText, SKILLS_SH } from './skillSearch'

const emit = defineEmits<{ choose: [result: SkillSearchResult, resolved: SkillSearchResolved] }>()

const session = useSession()
const announce = useAnnouncer()

const typed = ref('')
const query = ref<string | null>(null)
const results = ref<SkillSearchResult[] | null>(null)
const searching = ref(false)
const searchError = ref('')
let asked = 0
let timer: ReturnType<typeof setTimeout> | undefined

async function search(q: string) {
  const n = ++asked
  searching.value = true
  searchError.value = ''
  try {
    const { data } = await searchSkills(session.client, q)
    if (n !== asked) return
    query.value = data.query
    results.value = data.results
    announce.polite(data.results.length === 0 ? 'skills.sh found no skill.' : `skills.sh found ${data.results.length} skills.`)
  } catch (e) {
    if (n !== asked) return
    results.value = null
    searchError.value = failureText(e, { lost: 'skills.sh could not be reached: no answer from omo.', refused: 'skills.sh could not be reached.' })
  } finally {
    if (n === asked) searching.value = false
  }
}

watch(typed, (now) => {
  clearTimeout(timer)
  const q = searchText(now)
  if (q === null) {
    asked++
    searching.value = false
    searchError.value = ''
    results.value = null
    query.value = null
    return
  }
  timer = setTimeout(() => void search(q), SEARCH_DELAY)
})
onBeforeUnmount(() => {
  clearTimeout(timer)
  asked++
})

function retry() {
  const q = searchText(typed.value)
  if (q !== null) void search(q)
}

// Use

const choosing = ref<string | null>(null)
const chooseError = ref<{ id: string; text: string } | null>(null)

async function choose(r: SkillSearchResult) {
  if (choosing.value !== null) return
  choosing.value = r.id
  chooseError.value = null
  try {
    const { data } = await resolveSkillSearch(session.client, { source: r.source, skill_id: r.skill_id, name: r.name })
    emit('choose', r, data)
  } catch (e) {
    chooseError.value = { id: r.id, text: failureText(e, { lost: 'No answer from omo; try again.', refused: 'omo could not look at the repository.' }) }
    announce.assertive(visibleText(chooseError.value.text))
  } finally {
    choosing.value = null
  }
}

const shownQuery = computed(() => visibleText(query.value ?? ''))
</script>

<template>
  <FormSection legend="Search skills.sh" data-skill-search>
    <template #description>
      <p class="search__desc">Find a skill someone published and add it as a skill set; you choose its roles and save it yourself.</p>
      <p class="search__desc" data-skill-search-note>
        Powered by <a :href="SKILLS_SH" target="_blank" rel="noreferrer noopener">skills.sh</a>; omo is not affiliated with skills.sh or Vercel. Searching
        sends the search text, and nothing else, from omo to skills.sh.
      </p>
    </template>
    <TextField
      v-model="typed"
      label="Search"
      type="search"
      :hint="`At least ${MIN_SEARCH} characters.`"
      :maxlength="MAX_SEARCH"
      spellcheck="false"
      autocomplete="off"
      autocapitalize="off"
      data-skill-search-field
    />
    <p v-if="searching" class="search__note" data-skill-search-busy>Searching skills.sh…</p>
    <Callout v-else-if="searchError" tone="danger" live="assertive" data-skill-search-error>
      <p>{{ visibleText(searchError) }}</p>
      <template #actions><UiButton size="sm" data-skill-search-retry @click="retry()">Try again</UiButton></template>
    </Callout>
    <p v-else-if="results !== null && results.length === 0" class="search__note" data-skill-search-empty>skills.sh found no skill for “{{ shownQuery }}”.</p>
    <ul v-if="results !== null && results.length > 0" class="search__list" :aria-label="`Skills found on skills.sh for ${shownQuery}`" data-skill-search-results>
      <li v-for="r in results" :key="r.id" class="search__row" :data-skill-result="r.id">
        <div class="search__main">
          <p class="search__name" data-skill-result-name>{{ visibleText(r.name) }}</p>
          <p class="search__from">
            <span data-skill-result-source>{{ visibleText(r.source) }}</span> · <span data-skill-result-installs>{{ installWords(r.installs) }}</span> ·
            <a :href="r.url" target="_blank" rel="noreferrer noopener" data-skill-result-link
              >on skills.sh<span class="sr-only">{{ ' ' + visibleText(r.name) }}</span></a
            >
          </p>
          <Callout v-if="chooseError?.id === r.id" tone="danger" data-skill-result-error>
            <p>{{ visibleText(chooseError.text) }}</p>
          </Callout>
        </div>
        <UiButton size="sm" :disabled="choosing !== null" :aria-busy="choosing === r.id ? 'true' : undefined" data-skill-result-use @click="choose(r)">
          {{ choosing === r.id ? 'Looking at the repository…' : 'Use' }}<span class="sr-only">{{ ' ' + visibleText(r.name) }}</span>
        </UiButton>
      </li>
    </ul>
  </FormSection>
</template>

<style scoped>
.search__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.search__desc {
  margin: 0;
}
.search__list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.search__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.search__row > :deep(button) {
  flex-shrink: 0;
}
.search__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.search__main p {
  margin: 0;
  overflow-wrap: anywhere;
}
.search__name {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.search__from {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
