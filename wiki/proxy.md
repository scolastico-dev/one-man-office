# Web UI forwards

A forward lets you open a web UI running on the omo host through the board's
existing address and port, including from another machine. The target can stay
on loopback. No tunnel or additional public listener is needed. omo registers
an address; you or an agent start and stop the target process separately.

## Open and manage forwards

On a ticket, find **Forwards** beneath the description in the panel, or in
**Overview** on the full page.
Choose **Add forward**, enter its name and target port, then copy the returned
base path before starting the app. **Open** opens its start path in a new
browser tab. **Remove** unregisters it without stopping the app. The topbar's
**Forwards** button lists all ticket and global forwards, with the same actions
and links to their tickets. A target that is offline stays listed; opening it
shows an explanation with a link back to the board. TLS connection failures
also show that page.

A ticket forward belongs to that exact ticket. Completing or cancelling the
ticket automatically removes its forwards, including when a task completes
by merging into its story. Do not rely on a task's preview for later story
review. Generic global forwards have no ticket and stay until the user removes them.
Plugin-owned globals instead follow their plugin/task lifecycle (below).
A removed forward stops accepting new requests; registration is not process
supervision and does not terminate an already upgraded connection.

Agents register for their own ticket by default; they can add/remove only
that ticket's forwards. Users may manage all forwards, including globals.
List results follow the caller's ticket read scope.

```sh
# Agent: own ticket. User in their own shell: global unless --ticket is set.
omo ai proxy add --port 5173 --name "dev server"
omo ai proxy list
omo ai proxy list --ticket 12
omo ai proxy remove FORWARD_ID

# User: a named ticket, another host, HTTPS and certificate verification.
omo ai proxy add --ticket 12 --host devbox.example --port 8443 \
  --scheme https --verify-tls --name "tool" --start-path '/demo?view=compact'

# Relative-address app: remove the prefix on the upstream request.
omo ai proxy add --port 3000 --name "relative app" --strip-prefix
```

The MCP equivalents are `proxy_add`, `proxy_list` and `proxy_remove`, with
`ticket`, `host`, `port`, `scheme`, `name`, `start_path`, `strip_prefix` and
`verify_tls` fields on add. They use the same definitions and permissions as
the CLI. Omit `ticket` for the agent's own ticket; an agent cannot create a
global forward. List accepts an optional ticket; remove takes an `id`.

Only port and name are required. The target defaults to `http://127.0.0.1`,
start path `/`, `strip_prefix=false`, `verify_tls=false`. Any target host/IP
(including IPv6) and port 1–65535 are allowed; there is no target allowlist.
An HTTPS target's self-signed certificate is accepted by default; enable
`--verify-tls` / **Verify TLS** to require normal certificate validation.
Registration does not check whether the target is listening.

## Path mode and app configuration

Add/list return an `address` and `base_path`, such as
`/proxy/0123456789abcdef0123456789abcdef/`. In path mode the app receives that
prefix unchanged by default. Register first, configure that exact base, then
start the dev server on its registered port. Name the forward in the hand-in
so the user can choose Open; the displayed address alone does not sign in.
The CLI address is a server-side hint; a remote browser must use the reachable
board host rather than a loopback-only `omo.localhost` hint.

For Vite, use the returned base path, including its trailing slash:

```sh
npm run dev -- --host 127.0.0.1 --port 5173 --strictPort \
  --base /proxy/0123456789abcdef0123456789abcdef/
```

Vite's [base option](https://vite.dev/config/shared-options#base) also applies
in its config file. Keep HMR URLs on the browser's omo host/port; omo passes
WebSocket upgrades through. If your app specifies a separate client host or
port, update it for the public address rather than exposing the target port.
Consult your installed Vite version's [server options](https://vite.dev/config/server-options)
for its WebSocket and Origin settings. omo rewrites upstream Host to the
target; path-mode app Origin checks remain the app's responsibility.

For Next.js, set the [basePath](https://nextjs.org/docs/app/api-reference/config/next-config-js/basePath)
without the final slash before starting/building it:

```js
// next.config.js — substitute the id returned by registration.
module.exports = {
  basePath: '/proxy/0123456789abcdef0123456789abcdef',
  trailingSlash: true,
}
```

`trailingSlash: true` keeps the prefix root in its slash form. Use the base in
app-owned fetches, public image paths and custom WebSocket URLs too; omo does
not rewrite HTML or JavaScript. Framework base settings do not automatically
fix every hard-coded root address.

With **Strip prefix** / `--strip-prefix`, the browser still uses
`/proxy/<id>/…`, but the target receives `/…`. Use relative links/assets or a
framework that can separate its external base from its server routing.
`X-Forwarded-Prefix` names the removed prefix only in this mode. A framework
configured to serve the prefix itself should keep the default instead.

### Fallbacks and limits

For apps that ignore the base, requests outside `/proxy/` whose same-origin
`Referer` is under `/proxy/<id>/` receive a **307** redirect under that forward.
This includes root `/assets/…` and `/api/…`, ahead of board HTTP routes. Method,
body and query survive. Use strip-prefix for an upstream that serves at root.
A browser/referrer policy that omits the Referer prevents this fallback.
omo's own error and sign-in pages suppress Referer and use `no-store`, so
their **Back to board** links reach the board without entering the fallback.

WebSockets usually have no Referer. Unprefixed WebSocket requests use the
last-opened forward remembered in the browser's HttpOnly cookie. Opening or
navigating another forward updates it: multiple apps without base paths can
therefore send their WebSockets to the wrong app. Prefer explicit prefixed
WebSocket URLs or subdomains. These exact board WebSocket endpoints stay on
the board, even with a proxy Referer or last-forward cookie:

- `GET /api/agents/{id}/terminal`
- `GET /api/terminals/{id}/socket`

The internal `/_omo/proxy/` bootstrap/exchange routes also stay reserved.
Other app `/api` WebSockets can use the fallback. Normal board navigation,
API requests and `/api/events` without an app Referer retain board routing,
authentication and security headers.

If an app changes the address bar outside the prefix with `history.pushState`
or similar, a reload loses the forward context and may reach the board.
Fallbacks are a compatibility aid; a correct app base or subdomain is more
reliable. No iframe, service worker, page rewriting or injected app script is
used. Responses retain the app's own security/cache headers. Redirects to the
target and applicable cookie paths are rewritten into the forward; responses,
uploads/downloads, SSE and WebSockets stream without whole-body buffering.

## Optional subdomains

Subdomain mode serves the same forward at
`<prefix><id><suffix>.<proxy_base_host>` on the existing listener/external
port. Paths and query bytes reach the target without a prefix, regardless of
strip-prefix. Configure the app at `/` for this address. Open and add/list
prefer it; `base_path` still names the always-available path URL. One app
configured for `/` may need strip-prefix for its path URL too; an app serving
a prefixed base needs reconfiguration to work at the subdomain root.

For local browser access:

```sh
omo config set proxy_base_host omo.localhost
omo config set proxy_label_prefix dev-
omo config set -- proxy_label_suffix -user123
# Restart omo to apply server start options.
```

A forward then opens as
`http://dev-0123456789abcdef0123456789abcdef-user123.omo.localhost:8090/`.
The forced prefix/suffix are required: the bare id host is refused. Unset the
prefix/suffix for `<id>.omo.localhost`, or `proxy_base_host` to disable
subdomains. The base itself remains the board.

`*.localhost` resolves to the **browser's own machine**. This example works
locally; it cannot direct a remote user's browser to the omo host. For remote
access use a DNS base you control, for example `apps.example.com`, with base
and wildcard DNS records pointing to the omo host/reverse proxy. Every client
must resolve those names and reach the existing external port. With HTTPS,
route both board/base and wildcard forward hosts to omo and provide matching
certificates. A record on the server alone does not configure remote DNS.

The base must have at least two DNS labels and must be usable for browser
Domain cookies, not a public suffix such as `com` or `co.uk`. Validation
rejects IPs, wildcard text, schemes, ports, paths and trailing dots. Prefix +
the 32-hex id + suffix must form one DNS label of at most 63 ASCII bytes:
letters/digits/hyphens, with alphanumeric ends. The full host is at most 253
bytes. Matching is case-insensitive; config set lowercases the settings.
Only the exact composition and the listener or `public_url` external port are
accepted; extra labels, wrong affixes and other ports are refused. Unknown
well-formed ids show the missing-forward page after sign-in. Exact board
aliases in `allowed_hosts`/`public_url` must not collide with a composed host.

## Sign-in and trust

With token sign-in, choose **Open** from the signed-in board. It obtains a
random single-use grant that expires after 60 seconds, navigates the new tab
to omo's bootstrap page with the grant in a fragment, clears that fragment
before exchanging it, then replaces the page with the server-owned start
URL. Neither the board bearer token nor a retained grant appears in the final
address. Opening a direct forward URL without a proxy session shows a page
linking back to the board; sign in there and choose Open.

The separate proxy session cookie is HttpOnly, SameSite=Lax, lasts 12 hours
without sliding expiry and is invalidated by a server restart. In path mode
it is host-only; with subdomains it covers the configured base and its
subdomains. Open also establishes host-local path access on an unrelated
board host. Every new request checks that the forward still exists. Basic
mode checks the board's Basic credentials on each proxy request; none mode
adds no sign-in. Proxy cookies do not authenticate the board API.

Removing a forward or completing/cancelling its ticket prevents new proxied
requests. Existing WebSocket, SSE and other streaming connections are not
forcibly closed; they can continue until the app or client closes them.

omo removes its reserved cookies, recognized omo Authorization, internal
identity headers and omo WebSocket token protocols before forwarding. App
cookies and unrelated app Authorization remain. Apps are **trusted**: path
apps share the board's origin and localStorage. Upstream headers such as
`Service-Worker-Allowed` and `Clear-Site-Data` pass through too: a trusted app
can install a service worker with broader origin scope or clear origin data,
with effects that can persist after its forward is removed. omo itself does
not install a service worker. Subdomains do not promise
cookie isolation either: an app can set Domain cookies covering the base;
JavaScript can plant duplicate reserved cookies, which omo rejects by failing
closed. Register only trusted apps; this is not a sandbox. See [Security](security.md).

Use one canonical `public_url` for board access and reliable **Back to board**
links. Without it, a subdomain's back link uses the base host, which may be a
different browser origin with separate board token storage. If you use the
base as a board address behind HTTPS termination, configure it as `public_url`
or list it in `allowed_hosts` as well: accepting its Host for routing does
not bypass the board's existing HTTPS Origin checks.

## Behind an HTTPS reverse proxy

Set the external board origin, including a nondefault port if needed:

```sh
omo config set public_url https://office.example.com/
```

omo itself keeps its existing plain HTTP listener. The configured public
origin determines Open's external scheme/port, forwarding context and Secure
proxy cookies behind TLS termination. It does not change DNS or listener
settings. Arbitrary client `X-Forwarded-*` headers cannot override it.
Keep omo's backend private and preserve the original Host and path/query.
omo must be served at the origin root, not beneath another prefix.

Your reverse proxy must support WebSocket upgrades and long-lived responses
for `/proxy/…` and all configured forward hosts, as well as board terminals.
Disable response buffering for app SSE/streaming and `/api/events`, allow
suitable read timeouts, and avoid buffering large request bodies when upload
streaming matters. Check its body limits too. Do not inject board-only CSP
or no-store headers into app responses. These requirements apply to the
outer proxy even though omo itself streams. With subdomains, pass the
wildcard Host unchanged and use wildcard routing/certificates on the same
external port. See [Running omo](running.md#behind-a-reverse-proxy) for the
board's deployment settings.

## Plugin ownership and activity

Plugins register through bound Lua `forward_add`, `forward_list` and
`forward_remove` helpers, using this same registry, validation, transport and
Open/sign-in. A plugin forward carries optional `plugin` and `task_id` metadata.
Only server-bound helpers assign ownership; public create JSON rejects those
fields. The user can still view/remove these forwards normally. Helpers cannot
list/remove another plugin's forward or attach another plugin's task.

Stable background-task attachment removes the forward on every task exit,
including natural exit and idle stop. Unattached plugin resources end on
disable/remove, code replacement and shutdown too. Restart removes stale
plugin-owned records before loading hooks. Generic persistent forwards are
preserved. Registration and attachment emit normal board events, including
`proxy.forward_updated` for the first task binding.

Plugins can inspect authenticated request/WebSocket counts and schedule a
plugin-chosen idle duration for an attached task. Accepted WebSockets remain
active for their entire downstream lifetime, including hijacked connections;
only the last disconnect starts the next idle interval. Reconnect invalidates
old deadlines atomically with upgrade admission. Authentication failures,
refused upgrades and health/status polling cannot keep an idle task alive.
HTTP/SSE requests have separate request counts and do not serve as WebSocket
presence. Connection counts reflect server-observed closure; editor/protocol
heartbeats and network detection bound observation of physically dropped clients.
No editor-specific default or automatic idle policy is built into the proxy.
See [Plugins — Owned Web UI forwards](plugins.md#owned-web-ui-forwards) for the
exact helpers, schema, timing semantics and free-loopback-port candidate contract.
Always use user-only Open for the browser destination; helper addresses never
contain a login token or substitute for sign-in.
