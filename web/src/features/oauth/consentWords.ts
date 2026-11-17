// What the consent page says about a client that asks for access to omo's
// MCP endpoint. Such a client is tied to no omo agent session: the grant
// gives it only the relayed servers marked "Also available without an omo
// session", never omo's board or ticket actions.
import type { OAuthConsent } from '../../api/types'

/** What a connection without an omo session gets, the same words on the consent page and the MCP settings. */
export const WITHOUT_SESSION_GETS =
  'A connection that is not an omo agent session gets only the relayed servers marked "Also available without an omo session". It gets no board or ticket actions; those stay with the agents omo starts and the omo ai command line.'

/** How omo knows the client, in words. */
export function clientKindWords(c: Pick<OAuthConsent, 'client_kind'>): string {
  return c.client_kind === 'cimd'
    ? 'It describes itself with a document at its own https address (its client id).'
    : 'It registered itself with omo a moment ago; omo cannot check who made it.'
}

/** The warnings of a request, empty when there is none to give. */
export function consentWarnings(c: Pick<OAuthConsent, 'client_kind' | 'localhost_redirect'>): string[] {
  const out: string[] = []
  if (c.client_kind === 'registered') out.push('The name above is what the program says about itself; any program can register under any name. Approve only if you just started this sign-in yourself.')
  if (c.localhost_redirect)
    out.push('The answer goes to a program on the computer of the browser (a localhost address). Any program running there could have asked; approve only if you started this sign-in yourself.')
  return out
}
