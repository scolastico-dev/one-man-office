// The invented product the README's screenshots show: Tallyfox, invoicing
// and time tracking for freelancers, in four repositories. Everything here is
// demo content: the board's tickets (for the seeding tool), the comments, and
// what the scripted agents print into their terminals.
import { script } from '../support/agents'

/** The repositories, created empty in the run's temporary folder. */
export const PROJECTS = ['tallyfox-api', 'tallyfox-web', 'tallyfox-mobile', 'tallyfox-docs'] as const

/** In-work limits while the screenshots are taken: what is beyond them waits in Ready For Dev. */
export const LIMITS = { tasks: 5, stories: 1 }

interface Ticket {
  ref: string
  type: 'story' | 'task'
  title: string
  description: string
  projects: string[]
  story?: string
  review?: 'skipped' | 'agent' | 'human' | 'agent_then_human'
  when_approved?: 'merge' | 'export'
  to?: 'planning' | 'ready' | 'in_progress' | 'review' | 'done'
  /** A branch the run creates in the ticket's repository, with `files` committed on it. */
  branch?: { name: string; files: Record<string, string>; message: string }
}

const RATE_LIMIT = `Anyone with the link can open an invoice at \`/i/{token}\` and download its PDF. Nothing limits how often, so a leaked link can be hammered and the PDF renderer is expensive.

## What to do

- Limit both routes per client IP (token bucket), configurable as \`public.rate\` and \`public.burst\`.
- Answer with **429** and a \`Retry-After\` header when the bucket is empty.
- Logged-in requests from the web app are not limited.

## Done when

- [ ] Both routes share one limiter
- [ ] A test covers the 429 and the header
- [ ] The defaults are documented in \`config.example.yaml\``

/**
 * The board, in the order the seeding tool creates it (the numbers follow
 * this order). Work is seeded in Ready For Dev and started by omo itself, so
 * every agent is a live one: the story's product manager and, within
 * LIMITS, the developers of the standalone tasks; the story's tasks wait for
 * a slot.
 */
export const TICKETS: Ticket[] = [
  // Done.
  { ref: 'vat', type: 'task', title: 'Fix VAT rounding on multi-currency invoices', description: 'Round per line, not on the total, as the tax office expects.', projects: ['tallyfox-api'], review: 'human', when_approved: 'merge', to: 'done' },
  { ref: 'dark', type: 'task', title: 'Dark mode for the invoice editor', description: 'Use the theme tokens in the editor and the PDF preview frame.', projects: ['tallyfox-web'], review: 'human', when_approved: 'merge', to: 'done' },
  { ref: 'webhook', type: 'task', title: 'Document the webhook signature check', description: 'Show how to verify `X-Tallyfox-Signature` in Node and Go.', projects: ['tallyfox-docs'], review: 'human', when_approved: 'export', to: 'done' },
  // Waiting for the user's review.
  { ref: 'zugferd', type: 'task', title: 'Export invoices as ZUGFeRD PDF', description: 'Embed the XML invoice into the PDF/A-3 so German clients can book it automatically.', projects: ['tallyfox-api'], review: 'human', to: 'review',
    branch: { name: 'omo/export-invoices-as-zugferd-pdf', message: 'feat(pdf): embed the ZUGFeRD XML into the invoice PDF', files: { 'internal/pdf/zugferd.go': 'package pdf\n\n// EmbedZUGFeRD attaches the EN 16931 XML to a PDF/A-3 invoice.\nfunc EmbedZUGFeRD(pdf, xml []byte) ([]byte, error) {\n\treturn attach(pdf, "factur-x.xml", xml)\n}\n' } } },
  // Reviewed by an agent right now.
  { ref: 'overdue', type: 'task', title: 'Overdue badge in the client list', description: 'Show how many invoices of a client are overdue, next to the name.', projects: ['tallyfox-web'], review: 'agent_then_human', to: 'review',
    branch: { name: 'omo/overdue-badge-in-the-client-list', message: 'feat(clients): show the overdue count next to the name', files: { 'src/clients/OverdueBadge.vue': '<script setup lang="ts">\ndefineProps<{ count: number }>()\n</script>\n\n<template>\n  <span v-if="count > 0" class="badge badge--warn">{{ count }} overdue</span>\n</template>\n' } } },
  // A story with its tasks.
  {
    ref: 'recurring',
    type: 'story',
    title: 'Recurring invoices',
    description: 'Freelancers with retainers want an invoice that repeats every month without copying the last one by hand.',
    projects: ['tallyfox-api', 'tallyfox-web'],
    to: 'ready',
  },
  { ref: 'schedule', type: 'task', title: 'Schedule recurring invoices', description: 'A daily job creates the invoices that are due and sends them.', projects: ['tallyfox-api'], story: 'recurring', to: 'ready' },
  { ref: 'repeat-ui', type: 'task', title: 'Repeat settings on the invoice page', description: 'Monthly or quarterly, start date, optional end date.', projects: ['tallyfox-web'], story: 'recurring', to: 'ready' },
  { ref: 'reminder', type: 'task', title: 'Remind the client before a repeat is sent', description: 'An email three days ahead, with a link to pause it.', projects: ['tallyfox-api'], story: 'recurring', to: 'planning' },
  // Standalone tasks omo starts.
  { ref: 'ratelimit', type: 'task', title: 'Rate-limit the public invoice links', description: RATE_LIMIT, projects: ['tallyfox-api'], to: 'ready' },
  { ref: 'offline', type: 'task', title: 'Offline time tracking in the mobile timer', description: 'Keep running timers and new entries on the device and sync them when back online.', projects: ['tallyfox-mobile'], to: 'ready' },
  { ref: 'pdf', type: 'task', title: 'Upgrade the PDF renderer', description: 'Move to the current major version; check the invoice templates still match.', projects: ['tallyfox-api'], to: 'ready' },
  // Planning.
  {
    ref: 'portal',
    type: 'story',
    title: 'Client portal',
    description: 'Clients log in to see all their invoices, download them and update their billing address.',
    projects: ['tallyfox-api', 'tallyfox-web'],
  },
  { ref: 'stripe', type: 'task', title: 'Stripe payment links in invoice emails', description: 'A "Pay now" button in the email that opens Stripe Checkout.', projects: ['tallyfox-api'] },
  { ref: 'onboarding', type: 'task', title: 'Rewrite the getting-started guide', description: 'Start with the first invoice, not with the settings.', projects: ['tallyfox-docs'] },
]

/** A task without a project, created through the API once omo runs. */
export const NO_PROJECT_TASK = { title: 'Compare EU e-invoicing providers', description: 'Peppol access points: prices, API quality, which countries they cover.' }

/** The ticket the ticket screenshot opens. */
export const TICKET_SHOT = 'ratelimit'

/** The user's comments, by ticket ref. */
export const COMMENTS: Record<string, string[]> = {
  ratelimit: ['Keep the limit generous: accountants open twenty invoices of one client in a row. 60 per minute with a burst of 20 is fine.'],
  zugferd: ['Checked the PDF with a ZUGFeRD validator: valid, profile EN 16931.'],
}

// ---- Terminals ----

const ESC = '\u001b'
const sgr = (code: string, s: string, off: string) => `${ESC}[${code}m${s}${ESC}[${off}m`
const bold = (s: string) => sgr('1', s, '22')
const dim = (s: string) => sgr('2', s, '22')
const green = (s: string) => sgr('32', s, '39')
const red = (s: string) => sgr('31', s, '39')
const cyan = (s: string) => sgr('36', s, '39')
const magenta = (s: string) => sgr('35', s, '39')

/**
 * The first line of a redrawn terminal: clears the screen and the scrollback
 * first, so what `ready` printed (the start prompt) is gone.
 */
const first = (text: string) => `print|${ESC}[2J${ESC}[3J${ESC}[H${text}`
const blank = 'print| '

const tool = (name: string, arg: string) => `print|${green('●')} ${bold(name)}(${arg})`
const result = (text: string) => `print|  ${dim('⎿')}  ${text}`
const say = (line: string, ...rest: string[]) => [`print|${green('●')} ${line}`, ...rest.map((l) => `print|  ${l}`)]
const status = (text: string) => `print|${magenta('✻')} ${text}`
const diff = (n: number, sign: '+' | '-' | ' ', code: string) =>
  `print|      ${dim(String(n).padStart(2))} ${sign === '+' ? green(`+ ${code}`) : sign === '-' ? red(`- ${code}`) : `  ${code}`}`

/** The developer: a coding session on the rate-limit ticket (it has to fit the terminal without scrolling). */
export const DEVELOPER = script(
  'ready',
  first(`${dim('> Work on ticket #$TICKET: the description and the comments say what is wanted.')}`),
  blank,
  tool('Read', 'internal/public/routes.go'),
  result('Read 58 lines'),
  tool('Search', 'pattern: "rate\\.Limiter", path: "internal"'),
  result('Found 1 file: internal/auth/login_limit.go'),
  blank,
  ...say('The login limiter counts per account. The public links need one per', 'client IP, shared by the page and the PDF: 60/min, burst 20, as the', 'comment asks.'),
  blank,
  tool('Write', 'internal/public/ratelimit.go'),
  result('Wrote 71 lines to internal/public/ratelimit.go'),
  tool('Update', 'internal/public/routes.go'),
  result('Updated with 3 additions and 2 removals'),
  diff(41, ' ', 'r := chi.NewRouter()'),
  diff(42, '-', 'r.Get("/i/{token}", h.showInvoice)'),
  diff(43, '-', 'r.Get("/i/{token}.pdf", h.invoicePDF)'),
  diff(42, '+', 'lim := newIPLimiter(cfg.Public.Rate, cfg.Public.Burst)'),
  diff(43, '+', 'r.With(lim.Middleware).Get("/i/{token}", h.showInvoice)'),
  diff(44, '+', 'r.With(lim.Middleware).Get("/i/{token}.pdf", h.invoicePDF)'),
  blank,
  tool('Bash', 'go test ./internal/public/...'),
  result(`${green('ok')}   tallyfox/internal/public   0.412s`),
  blank,
  status(`Writing the test for Retry-After… ${dim('(4m 12s · esc to interrupt)')}`),
  'hang',
)

/** The PM of a story: waiting for its tasks. */
export const PM = script(
  'ready',
  first(`${green('●')} The story has three tasks: two wait for a slot, the reminder`),
  'print|  needs your approval.',
  blank,
  status(`Waiting for the tasks of #$TICKET… ${dim('(esc to interrupt)')}`),
  'hang',
)

/** A reviewer: reading the diff of a ticket in review. */
export const REVIEWER = script(
  'ready',
  first(`${green('●')} ${bold('Bash')}(git diff main...HEAD --stat)`),
  result('1 file changed, 7 insertions(+)'),
  blank,
  status(`Reviewing the overdue badge… ${dim('(1m 03s · esc to interrupt)')}`),
  'hang',
)

/** What the CEO drafts: created (and left in Planning) before its terminal is redrawn. */
export const CEO_DRAFTS = [
  { type: 'story', project: 'tallyfox-api', title: 'SEPA direct debit payments' },
  { type: 'task', project: 'tallyfox-web', title: 'Mandate form on the public invoice page' },
  { type: 'task', project: 'tallyfox-docs', title: 'Explain direct debit in the payment docs' },
] as const

/** The CEO conversation (lines of at most 48 characters, the sidebar's width); `firstDraft` is the number the first draft gets. */
export function ceo(firstDraft: number): string {
  const n = (i: number) => bold(`#${firstDraft + i}`)
  return script(
    'ready',
    ...CEO_DRAFTS.map((d) => `create-ticket|${d.type}|${d.project}|${d.title}`),
    first(`${cyan('>')} We lose clients who want to pay by SEPA`),
    'print|  direct debit. Can we offer that next to',
    'print|  card payments?',
    blank,
    tool('Search', 'pattern: "PaymentMethod"'),
    result('Found 6 files'),
    blank,
    ...say(
      'Card payments already go through Stripe,',
      'and Stripe supports SEPA mandates: no new',
      'provider is needed. I drafted the work; it',
      'waits in Planning for your approval:',
    ),
    blank,
    `print|  ${n(0)} SEPA direct debit payments ${dim('(story)')}`,
    `print|  ${n(1)} Mandate form on the public invoice page`,
    `print|  ${n(2)} Explain direct debit in the payment docs`,
    blank,
    'print|  Approve them by moving them to Ready For',
    'print|  Dev, or tell me what to change.',
    blank,
    `print|${cyan('>')} `,
    'hang',
  )
}
