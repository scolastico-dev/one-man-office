# Native UI API

Native UI clients load the board from their configured omo server. These
additive endpoints support capability discovery and system notification
polling without a second notification queue, plus a desktop forward tunnel over
WebSockets. Shared Go wire types live in `internal/api/ui.go`. The board's `web/src/api/types.ts` mirrors
`UICapabilities`, `UINotification`, `UINotificationFeed` and
`UINotificationCursorReset`; `web/src/api/types.test.ts` guards field parity
with the Go API types. Notification IDs and all feed and recovery cursors
are strings in both languages.

## Authentication and access

All endpoints are **user-only**, over the normal API transport:

- Token mode: `Authorization: Bearer <login token>`.
- Basic mode: HTTP Basic authentication with the configured credentials.
- None mode: no credentials, as configured for the rest of the board.
- The local socket's ordinary user identity also works. Agent sessions,
  including the CEO, get `403 forbidden`.

Use the native HTTP backend with the configured board address and its real
Host header. Native requests omit `Origin` and `Sec-Fetch-Site`; no special
Origin or CORS exception is granted. Requests bearing a cross-site Origin
(including `tauri://localhost`) or `Sec-Fetch-Site: cross-site` are rejected
when Origin checking is enabled. Host validation still applies. Remote
addresses must satisfy the existing `public_url` / `allowed_hosts` rules.
Credentials belong in headers, never in a notification destination or URL.
API responses and the tunnel handshake return `Cache-Control: no-store`.
Forwarded application responses keep the application's own headers.

## Feature exchange

`POST /api/ui/capabilities`, `Content-Type: application/json`.

The UI sends its own supported features. For example, a client that can
poll and display system notifications sends:

```json
{"features":["ui.notifications.poll.v1","ui.notifications.system.v1"]}
```

The server returns its own list:

```json
{"features":["ui.capabilities.v1","ui.notifications.poll.v1","ui.forwards.tunnel.v1"]}
```

| Flag | Meaning |
|---|---|
| `ui.capabilities.v1` | This capability exchange contract is supported. |
| `ui.notifications.poll.v1` | The incremental GET feed below is supported. |
| `ui.forwards.tunnel.v1` | Root HTTP byte-stream tunnel to an existing registered forward. Advertised by the server and desktop native clients; never by Android. |
| `ui.notifications.system.v1` | A client can display native system notifications; this is a client flag, not a server promise. |

Unknown flag strings are harmless. An omitted `features` field, `null`, or
an empty array means the client advertises no features. The server does
not persist client lists or require matching flags or versions; one UI's
exchange does not change another UI's behavior. Clients ignore unknown
response fields and flags, and enable optional behavior only when both
sides support the required feature. On older servers, `404 not_found` for
this endpoint means discovery is unavailable; the client may still open
the board and disable notification polling and forward tunnels. Request fields
other than `features` are rejected by the API's usual strict JSON decoder.

## Registered-forward tunnel (desktop)

`GET /api/ui/forwards/{id}/tunnel` upgrades to a WebSocket with the required
subprotocol `ui.forwards.tunnel.v1`. Select the ID from the existing
`GET /api/proxy/forwards` registry. The server chooses the registered target;
the endpoint accepts no host, port or target URL. Authentication uses the
configured connection's token/Basic header, or no credentials in none mode.
Native clients omit Origin. The normal outer Host/Origin and user-only checks
apply before upgrade, including on external omo hosts. This board-owned socket
is reserved ahead of browser forward Referer/last-forward-cookie fallbacks.

### Wire contract

- One outer WebSocket represents one HTTP/1.1 connection, including sequential
  keepalive requests. Open independent tunnels for concurrent HTTP connections.
- Every application data message is **binary**, at most **65536 bytes**. Concatenate
  their payloads in order in each direction. Message boundaries have no meaning
  to HTTP and may bisect headers, bodies or application WebSocket frames.
  Empty binary messages contribute no bytes. Text messages and oversized messages
  fail the connection. Control ping/pong/close frames remain WebSocket controls.
  Compression is not negotiated.
- Send origin-form HTTP requests, e.g. `GET /assets/app.js HTTP/1.1` with the
  preview/local address in `Host`. Absolute request URLs and `CONNECT` are
  rejected with an inner HTTP 400. The app runs at `/` regardless of the forward's
  `base_path`, `strip_prefix` or `start_path`; consumers choose the initial path.
- HTTP response status, headers, chunked bodies, trailers and streaming data
  use ordinary HTTP/1.1 encoding. Uploads and responses stream with bounded
  buffers and backpressure, without collecting entire bodies. An application
  WebSocket starts with its normal inner HTTP upgrade; after its 101, carry its
  WebSocket wire bytes through the same binary byte stream. This is a nested
  WebSocket, with app frames independent of outer framing.
- HTTP `Connection: close` drains the final response then sends a normal
  WebSocket close, so close-delimited HTTP bodies end with clean EOF.
  Closing/dropping either outer endpoint, a protocol/I/O error, cancellation or
  server shutdown ends both directions and cancels active upstream work. There
  is no TCP half-close or reconnect/resume within a tunnel. A new connection
  starts a new tunnel; callers decide whether an interrupted request is safe
  to retry.

Missing/removed forwards return outer HTTP 404 before upgrade. Missing required
subprotocol returns HTTP 400; malformed upgrades return HTTP 400 or 426, and
authentication/access failures retain 401/403. Every inner HTTP request rechecks registry liveness;
a forward removed after the handshake returns inner 404 on the next request.
Existing SSE/upgrade streams retain the shared proxy's behavior: removing the
record does not forcibly close them. An upstream connection or TLS failure is
an inner HTTP 502 with the existing “Web UI unreachable” explanation, leaving
the forward registered.

The tunnel reuses the root/subdomain proxy data plane: the registered upstream
TLS verification setting, direct target pools, redirect/cookie rewriting and
plugin request/WebSocket activity all apply. Redirects to the registered target
use the inner app Host at its root; same-origin app WebSocket Origin is translated
to the target. Foreign app origins stay subject to the upstream's checks.
The outer board Authorization and cookies are never copied into inner requests.
Defensive filtering also removes recognized omo Authorization, reserved omo
cookies/headers/grant queries and `omo-token.*` app subprotocols from inner traffic;
ordinary app credentials survive. The accepted outer peer supplies client-IP
metadata. Board CSP/cache headers are not added to app responses.

### Reusable Rust API

`omo-ui-core` exports these entry points:

| Entry point | Contract |
|---|---|
| `BoardClient::capabilities(&self, android: bool) -> Result<ServerCapabilities>` | Blocking native capability exchange, independent of notification preferences. `notification_polling` and `forward_tunnel` are optional booleans; Android always reports tunnel support as false. |
| `tunnel_supported(status: u16, body: &[u8]) -> Result<bool>` | Parse the server tunnel flag, ignoring unknown flags/fields. Missing flags or HTTP 404 return false; other HTTP errors or invalid JSON are errors. Enable only alongside the client's own supported flags. |
| `ForwardConnector::new(config: &Config, secret: &str) -> Result<ForwardConnector>` | Validate the configured board origin/auth and retain handshake credentials only in native memory. Clone for independent connections. Available only on desktop. |
| `ForwardConnector::connect(&self, forward_id: &str) -> Result<ForwardTunnel>` (async) | Connect through the configured board's ws/wss endpoint, require negotiated v1 and validate the lowercase 32-hex registry ID. Requires a Tokio runtime with I/O/time enabled. A 20-second handshake budget does not limit the established stream. |
| `ForwardTunnel` | Tokio `AsyncRead + AsyncWrite + Unpin + Send`; usable with `tokio::io::split`, `copy_bidirectional`, or an inner WebSocket client. Writes emit bounded binary chunks; flush sends pending output. Shutdown closes both directions; normal/going-away close is EOF, error close codes remain failures; drop immediately releases the network connection without background workers. |

The native connector verifies the **board** TLS certificate using public roots,
never follows redirects and never sends traffic directly to the app. Handshake
HTTP failures report their status without echoing credentials or server bodies.
The app target's TLS policy is owned solely by the server's registered forward.
`features(android, notifications)` advertises the tunnel on desktop independently
of notifications. `BoardClient::connect` preserves its notification-enabled
boolean return; consumers use `capabilities` for tunnel discovery. Older servers
and unknown flags remain compatible; Android builds exclude the desktop connector.
The shared fixture `internal/api/testdata/forward_tunnel_v1.json` is exercised by
both Go server and native Rust tests, including arbitrary message splitting.

## Notification polling

`GET /api/ui/notifications?cursor=41&limit=200`

No request body or content type is required. Query fields:

| Field | Meaning |
|---|---|
| `cursor` | Last scanned committed event ID, exclusive, as a canonical decimal string in `0..9223372036854775807`. Omitted means `0`, replay from the start. An explicitly empty, repeated, signed, padded or malformed cursor is rejected. |
| `limit` | Committed **events scanned**, from 1 through 200. Omitted or `0` means 200. An out-of-range value is `422 validation`; malformed or repeated values are `400 bad_request`. |

Example response:

```json
{
  "notifications": [
    {
      "id": "event:42",
      "kind": "question",
      "title": "Question for you on #7",
      "body": "Which option should we use?",
      "destination": "/7?tab=comments&comment=18"
    }
  ],
  "next_cursor": "45",
  "has_more": false
}
```

The `notifications` array is always present, including when empty. Items
are in ascending committed event order. A page can be empty and still have
`has_more: true`: it scanned events that did not generate notifications.
Continue immediately with `next_cursor` until `has_more` is false. New
events arriving between requests remain reachable; pagination never jumps
over unscanned events. After catching up, poll at the client's chosen
interval. An idle poll returns the same cursor and an empty list.

Keep the cursor as a string, independently for each UI installation and
server identity. Do not coerce it to a JavaScript number. Persist the new
cursor only after accepting the entire page for delivery. If a response is
lost, retry the old cursor: notification IDs, content and destinations for
those events remain stable, even if a ticket was renamed, a question was
answered or the browser's bell was marked read in the meantime. Deduplicate
by ID scoped to the configured server. A lost response or a crash between
system delivery and cursor persistence can replay the same IDs; the server
does not claim exactly-once delivery to the operating system.

The feed is an event history, rather than a list of things still unresolved:

| `kind` | Trigger | Destination |
|---|---|---|
| `mention` | A newly committed non-user comment mentioning the user, including system comments. | `/<ticket>?tab=comments&comment=<comment>` |
| `question` | A newly committed non-user question addressed to the user. A question that also mentions the user yields one item, of this kind. | `/<ticket>?tab=comments&comment=<comment>` |
| `attention` | A committed ticket summary enters the bell's attention category: attention is set and its supervisor is the user. | `/<ticket>` |
| `review` | A committed ticket summary enters the bell's review category: human review, with neither user-supervised attention nor open questions for the user taking precedence. | `/<ticket>` |

Repeated summaries in the same category produce no further ticket notice;
leaving and re-entering a category does. As in the bell, attention takes
precedence over questions, which take precedence over review. Questions
are delivered from comment events. The categories are determined from
stored `ticket.created`, `ticket.updated`, `ticket.moved` and
`comment.created` payloads. Ticket categories compare against the preceding
retained summary for that ticket, across page boundaries. If no preceding
summary survives, the first retained summary establishes its category.
An attention transition and a separate system comment mentioning the user
are separate committed events and can both produce notifications.

Titles and bodies are plain display text, never HTML or executable content.
Comment bodies retain their stored text; ticket notices use the stored
ticket title as their body. Clients render and, if needed, shorten/sanitize
text for their operating system. Resolve `destination` against the
configured board origin, not against the app's setup page. It is always a
board-relative path, with no server credentials or external host.

### Cursor errors and recovery

Malformed cursors return `400 bad_request`. A cursor ahead of the event
log, older than retained history, or missing inside retained history
returns `409 conflict` with recovery information:

```json
{
  "error": {
    "code": "conflict",
    "message": "notification cursor is outside retained event history; choose a recovery cursor explicitly",
    "details": {"reset_cursor":"100","latest_cursor":"350"}
  }
}
```

Choose recovery explicitly. `reset_cursor` is immediately before the
oldest retained event; retry it to replay retained history, deduplicating
IDs. `latest_cursor` is the committed event high-water mark; use it to
start after existing history. This can also establish a baseline for an
installation that should notify only about future events: request with
`cursor=9223372036854775807`, read the conflict's `latest_cursor`, then poll
from there. Events committed after that response remain reachable.
Unavailable history cannot be recovered by either cursor. If all events
were removed, both recovery cursors point to the retained AUTOINCREMENT
high-water mark. A new, empty board instead starts at `0`. Recovery
boundaries are valid even when no event currently has that exact ID.
The server currently keeps its event log; this handling also covers
truncated/imported history. Replacing a server's database should be treated
as a new server identity when storing client cursors and deduplication IDs.

### Independence from the browser bell

Polling never writes server delivery state, emits an event, consumes
another caller's notifications, or reads/changes the bell's marker.
`GET /api/notifications` and `PUT /api/notifications/seen` keep their
existing mention-list and user-reading semantics. Reading the bell also
never consumes this feed. Capability exchange does not change the browser
client or its desktop notification setting.
