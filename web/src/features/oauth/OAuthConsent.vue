<script setup lang="ts">
// The OAuth consent page (/oauth/authorize): a program asks for access to
// omo's MCP endpoint. omo checks the request first (client, its exact
// return address, PKCE, the resource) and the page shows who asks, where
// the browser goes after the answer, a warning for a client omo cannot
// check, and exactly what the grant gives: only the relayed servers marked
// "Also available without an omo session", no board or ticket actions.
// Approve and Deny send the user's answer and go where omo says. Every text
// of the client is shown as visible text.
import { computed, onMounted, ref, useId } from 'vue'
import { decideOAuthConsent, getMCPServers, getOAuthConsent } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { OAuthConsent } from '../../api/types'
import Callout from '../../components/ui/Callout.vue'
import Logo from '../../components/ui/Logo.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { visibleText } from '../../components/ui/visibleText'
import { boardLocation, settingsLocation } from '../../locations'
import { useSession } from '../../session'
import { WITHOUT_SESSION_GETS, clientKindWords, consentWarnings } from './consentWords'

const props = withDefaults(
  defineProps<{
    /** The query of the authorization request; the page's own address by default. */
    query?: string
    /** Goes to the address omo answered; the browser's navigation by default. */
    go?: (url: string) => void
  }>(),
  { query: undefined, go: undefined },
)

const session = useSession()
const announce = useAnnouncer()
const headingId = useId()
const query = props.query ?? window.location.search.replace(/^\?/, '')

const consent = ref<OAuthConsent | null>(null)
const loadError = ref('')
/** The relayed servers the grant gives, when omo could list them. */
const shared = ref<string[] | null>(null)
const answering = ref<'approve' | 'deny' | null>(null)
const answerError = ref('')
const leaving = ref('')

onMounted(async () => {
  try {
    const { data } = await getOAuthConsent(session.client, query)
    consent.value = data
  } catch (e) {
    loadError.value = failureText(e, { lost: 'No answer from omo. Reload the page to try again.', refused: 'omo refused this sign-in request.' })
    return
  }
  try {
    const { data } = await getMCPServers(session.client)
    shared.value = (data.servers ?? []).filter((s) => s.global && s.enabled).map((s) => s.name)
  } catch {
    shared.value = null
  }
})

const warnings = computed(() => (consent.value ? consentWarnings(consent.value) : []))
const from = { query: {} }

async function answer(approve: boolean) {
  if (answering.value) return
  answering.value = approve ? 'approve' : 'deny'
  answerError.value = ''
  try {
    const { data } = await decideOAuthConsent(session.client, query, approve)
    leaving.value = `${approve ? 'Approved' : 'Denied'}. Going back to ${visibleText(consent.value?.redirect_host ?? 'the program')}…`
    announce.polite(leaving.value)
    ;(props.go ?? ((u: string) => window.location.assign(u)))(data.redirect)
  } catch (e) {
    answerError.value = failureText(e, { lost: 'No answer from omo; start the sign-in in the program again.', refused: 'omo refused the answer.' })
    announce.assertive(visibleText(answerError.value))
    answering.value = null
  }
}
</script>

<template>
  <main class="consent" :aria-labelledby="headingId" data-oauth-consent>
    <section class="consent__box">
      <div class="consent__brand">
        <Logo :size="40" />
        <h1 :id="headingId" class="consent__title">Allow access to omo?</h1>
      </div>
      <p v-if="!consent && !loadError" class="consent__text">Checking the request…</p>
      <Callout v-if="loadError" tone="danger" live="assertive" title="This sign-in cannot go on" data-oauth-error>
        <p>{{ visibleText(loadError) }}</p>
        <p>Nothing was granted. Start the sign-in in the program again.</p>
      </Callout>

      <template v-if="consent">
        <p class="consent__lead">
          <strong class="consent__client" data-oauth-client>{{ visibleText(consent.client_name) }}</strong> asks to use omo's MCP endpoint.
        </p>
        <dl class="consent__facts">
          <div>
            <dt>After your answer the browser goes to</dt>
            <dd class="consent__host" data-oauth-redirect-host>{{ visibleText(consent.redirect_host) }}</dd>
          </div>
          <div>
            <dt>The program</dt>
            <dd data-oauth-kind>{{ clientKindWords(consent) }}</dd>
          </div>
          <div>
            <dt>Its client id</dt>
            <dd class="consent__id">{{ visibleText(consent.client_id) }}</dd>
          </div>
        </dl>
        <Callout v-for="(w, i) in warnings" :key="i" tone="warn" data-oauth-warning>
          <p>{{ w }}</p>
        </Callout>

        <section class="consent__grant" :aria-labelledby="`${headingId}-grant`" data-oauth-grant>
          <h2 :id="`${headingId}-grant`" class="consent__sub">What it gets</h2>
          <ul>
            <li>
              The tools of the MCP servers omo relays that are marked “Also available without an omo session”<template v-if="shared !== null"
                >: <template v-if="shared.length > 0"><span data-oauth-servers>{{ shared.map(visibleText).join(', ') }}</span></template
                ><template v-else>none at the moment</template></template
              >.
            </li>
            <li>No board or ticket actions: it cannot read, create, move or comment on tickets, and it never acts as one of omo's agents.</li>
            <li>The access lasts until you revoke it under Settings, MCP, Access, or until the program has not used it for 30 days.</li>
          </ul>
          <p class="consent__note">{{ WITHOUT_SESSION_GETS }}</p>
        </section>

        <Callout v-if="answerError" tone="danger" data-oauth-answer-error>
          <p>{{ visibleText(answerError) }}</p>
        </Callout>
        <p v-if="leaving" class="consent__text" role="status">{{ leaving }}</p>
        <div class="consent__buttons">
          <UiButton variant="primary" :disabled="answering !== null" :aria-busy="answering === 'approve' ? 'true' : undefined" data-oauth-approve @click="answer(true)">Approve</UiButton>
          <UiButton :disabled="answering !== null" :aria-busy="answering === 'deny' ? 'true' : undefined" data-oauth-deny @click="answer(false)">Deny</UiButton>
        </div>
      </template>
      <p class="consent__note">
        <RouterLink :to="boardLocation(from)">Back to the board</RouterLink> ·
        <RouterLink :to="settingsLocation('mcp', from)">MCP settings</RouterLink>
      </p>
    </section>
  </main>
</template>

<style scoped>
.consent {
  display: grid;
  place-items: center;
  min-height: 100dvh;
  padding: calc(var(--omo-space) * 4);
}
.consent__box {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  width: min(100%, 40rem);
  min-width: 0;
  padding: calc(var(--omo-space) * 6);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.consent__brand {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.consent__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  font-weight: 700;
  line-height: var(--omo-text-xl-lh);
}
.consent__lead,
.consent__text,
.consent__note,
.consent__grant ul {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.consent__lead {
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
}
.consent__client {
  color: var(--omo-accent);
}
.consent__note {
  color: var(--omo-muted);
}
.consent__facts {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
}
.consent__facts dt {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.consent__facts dd {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.consent__facts dd.consent__host {
  font-size: var(--omo-text-lg);
  font-weight: 700;
  line-height: var(--omo-text-lg-lh);
}
.consent__id {
  color: var(--omo-muted);
}
.consent__grant {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 3);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-raised);
}
.consent__grant ul {
  display: grid;
  gap: var(--omo-space);
  padding-left: calc(var(--omo-space) * 5);
}
.consent__sub {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
}
.consent__buttons {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
@media (max-width: 767.98px) {
  .consent {
    padding: calc(var(--omo-space) * 3);
  }
  .consent__box {
    padding: calc(var(--omo-space) * 4);
  }
}
</style>
