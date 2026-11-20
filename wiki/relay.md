# Relay

## Relay deployment

The standalone `omo-relay` executable connects outbound omo hosts and native UI
clients using the [version 1 protocol](relay-protocol.md). It serves only
`GET /v1/ws` and `GET /notifications/<host_id>/<ui_id>`. It does not run omo,
serve the board, or store content or notifications. All routing state disappears
on restart. Endpoint pairing, keys and notification receipt state belong to the
host and UI.

Build and run without Node.js or an omo home:

```sh
make relay
./bin/omo-relay -listen 127.0.0.1:8091
./bin/omo-relay -version
```

The default listen address is `:8091`. Admission is disabled unless
`-password-file` names a readable, nonempty file. The file contains just the
password, optionally followed by a newline (maximum file size 4096 bytes).
It is loaded once at startup; restart the relay to change it. Keep the file
private and provide its path, rather than a password on the command line:

```sh
./bin/omo-relay -listen 127.0.0.1:8091 -password-file /run/secrets/relay_password
```

With admission enabled, both routes require HTTP Basic username `relay` and
that password in the Authorization header. Missing or wrong credentials get
401 before upgrading or dispatching a notification. Credentials never belong
in URLs and are never forwarded to omo. Admission grants access to the relay;
endpoint authentication and encryption still happen between host and UI.

Build the separate container:

```sh
make relay-image
docker run --rm --read-only --tmpfs /tmp:rw,noexec,nosuid,size=1m \
  -p 127.0.0.1:8091:8091 omo-relay:local
```

The image runs only `/omo-relay`, as UID/GID 65532, with no writable persistent
data and no shell. For protected admission, mount an individual secret file
read-only, readable by UID 65532, and add
`-password-file /run/secrets/relay_password`. For example, with an existing
Docker secret or a local secret file provisioned for that UID:

```sh
docker run --rm --read-only --tmpfs /tmp:rw,noexec,nosuid,size=1m \
  --mount type=bind,src=/absolute/private/relay_password,dst=/run/secrets/relay_password,readonly \
  -p 127.0.0.1:8091:8091 omo-relay:local \
  -password-file /run/secrets/relay_password
```

For Compose, `docker compose -f docker/relay-compose.yaml up --build -d`
starts the relay on loopback port 8091. `RELAY_BIND` and `RELAY_PORT` change
the published address and port. To enable admission, add a Compose override
that mounts a secret and set `RELAY_PASSWORD_FILE` to its **container** path:

```yaml
services:
  relay:
    secrets:
      - relay_password
secrets:
  relay_password:
    file: /absolute/private/relay_password
```

```sh
RELAY_PASSWORD_FILE=/run/secrets/relay_password \
  docker compose -f docker/relay-compose.yaml -f relay-secret.yaml up --build -d
```

Ensure the secret file is readable by UID 65532; local Compose secrets are
file mounts. Stop and remove with the same `-f` options and `down -v`.
The existing omo container and entrypoint are separate and unchanged.

For remote use, put HTTPS/WSS termination in front of the relay with an ordinary
valid certificate. Preserve the Authorization and `Omo-Notification-*` headers,
support WebSocket upgrades, and allow long-lived connections. Do not log those
headers, passwords, or payloads. Configure proxy timeouts to allow the mandatory
20-second Ping interval and 10-second matching-Pong deadline. Notification GETs
wait up to 30 seconds; replies are `Cache-Control: no-store`. An unavailable
host returns empty 204, which is an availability hint, not authenticated proof
that there are no notifications. The host needs no inbound port or certificate.

Host IDs use first-live-registration ownership. Each UI has a separate random
routing handle; host loss closes its UIs. Slow destinations are disconnected
when their bounded writer queue fills. A lost Pong immediately removes the
affected socket and cancels its pending work. GETs use fresh random IDs scoped
to the current host registration; cancellation and timeout remove them and
send cancellation to a still-connected host. The relay never acknowledges or
consumes notifications.

The binary build writes `bin/RELAY_THIRD_PARTY_NOTICES`. The image includes
`LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES` under `/usr/share/doc/omo-relay/`.
This deployment does not publish a release image.

Focused verification:

```sh
go test ./cmd/omo-relay ./internal/relayserver ./internal/relayproto -count=1
CGO_ENABLED=1 go test -race ./cmd/omo-relay ./internal/relayserver -count=1
bash docker/relay_test.sh
```

The deployment test builds and runs the actual executable and image with and
without admission, verifies routing, wrong-password refusal, notification
forwarding and host loss, and removes its processes, containers, test image and
temporary files on exit. Containers use `--rm`, read-only roots and tmpfs;
only the individual read-only test secret is mounted from the host.

## Outbound relay host

omo can keep one outbound encrypted relay connection, preserving its host key,
routing name and paired devices across restarts. It opens no additional inbound
port and needs no host TLS certificate. The following sections describe host commands and
private state. The native UI is separate work. The host installs application and notification
consumers; absent consumer callbacks grant no access.
The exact wire and crypto contract is [Relay protocol](relay-protocol.md).

## Local human commands

Start omo, then run these commands in a human terminal on the host. All relay
administration goes through the local socket; browser login, paired remote
access and agent sessions cannot administer the relay.

```bash
omo relay connect https://relay.example --client-id office
# Or generate a new random routing name explicitly, preserving the host key:
omo relay connect https://relay.example --new-id
# Read an optional admission password privately from standard input:
omo relay connect https://relay.example --password-stdin
omo relay status
omo relay disconnect

omo relay pair begin
omo relay pair pending
omo relay pair accept <aid> <six-digit-PIN> --name phone
omo relay pair accept <aid> --public-key <full-unpadded-base64url-key> --name tablet
omo relay pair cancel
omo relay devices
omo relay unregister <ui-id>
```

Without either name flag, connect reuses the saved client id, or generates and
saves 16 random bytes as unpadded base64url on first use. A changed relay origin
or name is an explicit local operation; the UI must be configured for the same
route. A password is reused on the same origin when omitted, cleared when the
origin changes, and replaced when password-stdin is supplied (an empty value
clears it). Passwords never appear in URLs, status or device listings.

Only HTTPS origins are accepted, with ordinary certificate verification; HTTP
is allowed for explicit loopback development. Origins have no trailing slash,
path, credentials, query or fragment. DNS names must already be ASCII, including
IDNA A-labels. Name collisions keep the saved id: status reports collision and
retries after 1, 2, 4, 8, 16 and then 30 seconds, capped. Admission denial is
shown separately. The host Pings every 20 seconds and closes/redials when its
Pong is missing within 10 seconds, independently of application traffic.
Dial/upgrade and registration each have a 30-second deadline, so a silent peer
also triggers reconnection. At most 128 UI endpoints and 128 polls are admitted
on one host connection. Excess or duplicate items are refused individually.
Refusal/close replies are bounded and best effort: a full send queue may drop
that one reply while the control reader and other devices continue; a dropped
notification.empty leaves the relay's ordinary GET timeout to finish that poll.

Disconnect durably disables reconnection, closes live relay work and spends the
unfinished slot. It retains the host identity and paired devices. Enabled hosts
reconnect on restart; disabled hosts stay disabled. Config/state origin mismatch
and missing private identity pause connection until an explicit connect.

## Pairing and revocation

Pair begin opens one persisted five-minute ceremony for the whole host. It
replaces any unfinished ceremony with a visibly new attempt. The first UI
commitment freezes the attempt before the host sends its contribution. Reveal
must match it, then mutual ML-KEM proofs establish private-key possession.
Pending shows one attempt id, state and UI key fingerprint, never a PIN or a
list of candidates to search. Read the PIN in the native UI and type it on the
real host. The full-key alternative requires the complete raw 1184-byte UI
public key, encoded as unpadded base64url, plus the same possession proof.

Wrong PIN/key, invalid/changed proof, another attempt, cancellation, expiry,
disconnect or restart spends an unfinished slot. Remote retries cannot open it
again. Begin explicitly authorizes a new ceremony; it is not a retry of the old
commitment.

Successful local accept saves the device and notification root as active before
CLI success and encrypted pair.accepted. There is no extra UI confirmation step
or pair.confirm gate. Lost accepted replies recover by fresh authentication with
the exact saved device/key/epoch, even after restart. Neither network success
nor a timer authorizes an unaccepted device. The protocol's first-contact host
identity limitations still apply; see its trust section. The later UI must give
native pairing/connection screens distinct titlebar/chrome from forwarded pages.

Devices prints exact UI ids, local labels, host acceptance times, state and live
connection counts. Unregister durably removes that device before returning and
joins its sessions, stream callbacks and notification work. Other devices keep
working. Revocation works with the relay offline and blocks later authentication
and GET access. Pairing that key again requires a new locally accepted epoch.

## Private state and configuration

`server.yaml` contains `relay_url` and `relay_enabled` (default false). Use relay
connect/disconnect for immediate changes; ordinary config commands apply on the
next omo start. The manager saves private state before enabling configuration
and never generates a replacement identity merely because state is missing or
corrupt. Corruption, unknown state versions and failed disk saves fail closed.
Repair private state before restarting; deleting it loses host identity/trust.

`<OMO_HOME>/relay/state.json` is a bounded 4 MiB private file, mode 0600 with a
protected Windows ACL, in a plain private relay directory. It contains version
1, the 64-byte ML-KEM seed and host fingerprint, bound origin/id, admission
secret, global pairing slot, active devices and monotonic authorization
counter. Each device holds UI public key/id, epoch, original aid, notification
root, local label, acceptance time and generation. Notification state is an
independent cursor, exact last_ack receipt and optional pending receipt plus
ordered ids; notification bodies are not copied into relay state.

All updates use one server-owned transaction store with isolated copies and
atomic private replacement. CLI commands never edit this file concurrently.
Normal UI/host/relay restart preserves trust and receipt state. Settings/database
exports exclude the relay directory. An imported enabled server.yaml without
this private identity cannot enable an unknown host. Secure whole-home backup
rollback can restore revoked trust and older delivery state; that is an explicit
administrative rollback rather than normal restart behavior.

## Consumer interfaces

`internal/relayhost.OpenStore(dir)` opens the single store. `Store.Snapshot()`
returns an isolated State; `Store.Update(func(*State) error) error` serializes
copy/update/validation/durable replacement. Callback errors commit nothing;
disk save errors latch closed and cancel manager authority. One Manager owns a
Store; do not construct parallel managers or write private state elsewhere.

`NewManager(store, Options)` recovers unfinished pairing slots before remote
traffic. Server lifecycle calls `Manager.Run(ctx)` once and `Close()` on
shutdown. Local operations are Connect, Disconnect, Status, PairBegin,
PairPending, PairAccept, PairCancel, Devices and Unregister.
`Server.RelayManager()` exposes that manager for the downstream integrations.
Startup installs both `relayhost.Callbacks` before `Manager.Run`.
`Manager.SetCallbacks` replaces the complete struct; callers must retain both
hooks when replacing it:

```go
ServeSession func(context.Context, relayhost.Capability, relayproto.RecordIO) error
PollNotifications func(context.Context, relayhost.Device, relayproto.Poll) (relayproto.NotificationEnvelope, error)
```

The manager alone reads relay controls. It authenticates each active device and
completes reconnect ready/ready.ack before calling ServeSession. RecordIO Send
serializes seal-and-write globally per session; one callback Receive consumer
handles stream records. Close ends that endpoint. Callback contexts are cancelled
on disconnect/revoke/close; callbacks must stop and join their own work before
returning. Missing callbacks grant no application or notification access.

Capabilities have no exported minting fields. Use `Manager.Validate(cap)` at
request/stream admission; zero/foreign/revoked capabilities fail. Accessors are
UIID, PairEpoch, Generation and Context. Never derive a capability from a UI id
or forwarded header. Devices are private snapshots: explicitly project public
fields before exposing them, since they contain notification roots.

Notification callbacks run inside `Manager.WithDevice(ctx, uiID, fn)`, serializing
that device while leaving the global store unlocked during board/network work.
The manager checks active identity/epoch and verifies the poll MAC first.
`Manager.UpdateDevice(snapshot, func(*Device) error)` rechecks active generation
and changes only Cursor, LastAck and Pending, atomically. `Device.Cursor` is a
relayproto.Counter; LastAck is `*relayproto.Receipt`; Pending is
`*relayhost.PendingReceipt{Receipt relayproto.Receipt, IDs []relayproto.Counter}`.
Persist the exact full tuple; use Receipt.Equal for validated receipt comparison.
The callback implements the protocol's receipt transitions and seals its reply.
The manager rechecks authorization and socket scope before sending that response,
routes QID cancellation and joins the callback before unregister returns.

## Encrypted application transport

The host installs the application callback at startup after its application
routes exist. Authenticated devices can load the board APIs, assets, logs,
images and raw uploads, event streams and terminal WebSockets through the
relay. No additional host port is bound and no arbitrary upstream is dialed.
Each allocated relay stream becomes a virtual connection on a private
in-memory HTTP/1.1 listener. A real HTTP server preserves response flushing,
chunked request bodies and WebSocket Hijacker behavior; responses and
application JSON are not buffered or rewritten.

A native UI opens fresh sequential odd stream ids, starting at 1. It sends one
origin-form HTTP/1.1 request per stream, with Host `omo-relay.invalid` and either
no Origin or `http://omo-relay.invalid`. Connections do not persist for another
request. Upgraded connections remain bidirectional for terminal frames,
subprotocol `omo`, replay markers, input/message acknowledgments and close
frames. SSE reconnects in a new stream using the ordinary Last-Event-ID/resync
contract. Uncertain writes, including terminal input, are never automatically
replayed by this transport. The native UI itself is separate work.

Streams have at most 65,536 bytes of outstanding credit in each direction;
records carry at most 16,384 data bytes. Reading bytes grants credit, so a slow
consumer cannot create an unbounded receive queue. One demultiplexer and a
round-robin writer keep a stalled stream from blocking other streams. Each
session has 64 slots, including streams cancelling while their reciprocal
reset is still pending. FIN half-closes a direction. For an allocated stream,
reset cancels both directions and is reciprocated once with reset code
`closed` (or the reset already being sent). Both resets establish when
in-flight data/credit have drained and the slot can be freed. No payloads are
retained during cancellation and no old ids are arbitrarily evicted. A refusal
before stream.opened grants no byte credit and needs no reciprocal reset.
A peer that withholds reset completion can exhaust its own session's slots;
it cannot grow host memory or consume another device's slots.

## Remote application trust

Application authority comes only from a manager-created active device
capability with its exact pairing epoch, generation and cancellation context.
The host validates it at stream and HTTP-request admission, then explicitly
uses the human board actor and normal board/resource rules. Missing, foreign
or revoked authority is refused. Socket identity headers and browser login
credentials cannot supply this authority. Unregister closes and joins that
device's idle connections, event subscriptions and terminal viewers before
returning. It leaves other devices, underlying shells and agents running.

The tunnel rejects CONNECT, absolute/authority targets, another Host or Origin,
ambiguous paths (including encoded separators and nested escaping), identity
headers under `X-Omo-*`, Proxy-Authorization, relay admission/notification
credentials and `omo-token.*` WebSocket subprotocols. Presence counts even for
empty or repeated headers. Request trailers are refused. Authorization, Cookie
and forwarded identity headers are removed before dispatch.

Local `/api/relay/*` administration, `/mcp`, OAuth/admission endpoints and MCP
client token creation are unavailable remotely. Existing browser `/proxy/`
routing and subdomains remain separate; this transport adds no forwarded-site
support. Application requests retain the normal JSON requirement and raw
image/zip upload exceptions. Security headers and committed event ids follow
the ordinary application behavior.

Focused application verification:

```sh
go test ./internal/relaytransport -count=1
go test ./internal/server -run TestRelay -count=1
CGO_ENABLED=1 go test -race ./internal/relaytransport ./internal/server -run 'TestRelay|TestStream|TestHTTP|TestReset|TestBackpressure|TestProtocol|TestConnection|TestPartialCredit|TestRemoteReset|TestWriter' -count=1
```

These tests include the actual standalone relay, production host callback
wiring, local pairing, authenticated reconnect/restart, application HTTP,
chunked images, SSE replay/resync, terminal frames/acknowledgments and device
revocation. In-memory protocol peers cover quota violations, half-close,
deadlines, reset crossings/churn and bounded backpressure deterministically.

## On-demand device notifications

A paired UI can poll `GET /notifications/<host_id>/<ui_id>` without opening a
UI WebSocket. Send the protocol's `Omo-Notification-Request` and
`Omo-Notification-MAC` headers, generated with the device's persisted pairing
epoch and private notification root. Optional relay admission uses the separate
Basic Authorization header. A public key or routing ID alone grants no access.
The host verifies authentication before reading mentions or applying a receipt.

Online responses are HTTP 200 encrypted envelopes, including an empty list and
`invalid_ack` errors. Every response uses fresh randomness and a derived
AES-256-GCM key authenticated by the pairing root; the UI must check the host,
device, epoch and outstanding request ID when decrypting. HTTP 204 (offline,
unknown/revoked device, invalid authentication or timeout) is an untrusted
availability hint. It never proves that the host has no notifications and never
acknowledges a receipt.

Each device starts at cursor zero and independently receives retained comments
that mention the user, excluding the user's own comments, in ascending comment
ID order. Browser bell reads and browser seen updates do not suppress device
delivery. Pages contain at most 50 notices and 256 KiB encoded plaintext. Titles
are limited to 128 UTF-8 bytes, bodies to 384 bytes, each truncated at a code-point
boundary with its own truncation flag. Invalid UTF-8 from native tool output is
replaced before applying those byte limits. Encoded notices fit 4096 bytes even
with JSON escaping. Retrieve full comments through the application transport; an
oversized comment does not block later notices.

The host saves one pending receipt (batch ID, ordered comment IDs, through and
secret token) before returning it. Polling alone never advances the cursor.
Retries return that same receipt and ordered IDs, freshly encrypted and with
current bounded source previews. Deleted or no longer qualifying sources appear
as `{id,unavailable:true}`. Neither the relay nor the private receipt store caches
notification content. `more` reflects whether later qualifying sources exist at
the time of the source read.

The acknowledgment transitions compare the exact full receipt tuple:

| Request receipt | Host result |
| --- | --- |
| Null | Keep cursor, last acknowledgment and current pending batch; prepare one if needed. |
| Current pending receipt | Atomically advance the cursor, save that exact last acknowledgment and clear pending; then prepare the next batch. |
| Exact last acknowledgment | Acknowledge it again and return the current newer batch unchanged. |
| Any other tuple | Encrypted `invalid_ack`; no delivery state changes. |

Cursor, last acknowledgment and pending receipt survive normal host restart. If
acknowledgment X committed but the reply containing batch Y was lost, retrying X
returns `acknowledged:X` plus the unchanged Y receipt. If the host stopped after
saving X but before saving Y, retrying X prepares Y from the saved cursor.
Revocation and GET cancellation stop in-flight source work; the manager rechecks
authority before commits and replies, and unregister joins notification work.

The following is the contract for a future native UI, not a UI implementation:

1. Keep one durable receipt chain per host/device identity and one logical
   outstanding poll. Use a fresh request ID on retry and abandon superseded IDs.
2. Authenticate/decrypt the current reply, then durably store and deduplicate
   **all** its notices by comment ID before sending its receipt back.
3. Clear an outstanding receipt only when `acknowledged` matches its batch ID.
   Atomically store/deduplicate the returned next batch and replace the receipt
   with its tuple, or null for an authenticated empty page.
4. Retain the receipt after a lost, invalid or untrusted empty/error response.
   For an authenticated `invalid_ack` only, discard the stale receipt, retain
   received notices/IDs, and poll with ack null to recover the host's current
   pending batch. Deduplicate and acknowledge that batch normally.

Delivery is at least once until durable acknowledgment. A receipt from one
device cannot consume another device's notifications.

The production adapter is `server/relay_notifications.go`, using the board's
`RelayMentionsAfter(ctx, actor, after, limit, maxBytes)` and
`RelayMentionsByID(ctx, actor, ids)` consistent reads with `board.User`.
`relayhost.Manager.PollNotifications` consumes `NotificationSource` inside the
manager's existing per-device gate; callers must not nest another `WithDevice`.
The startup installs all callbacks together before `Manager.Run`, preserving
both consumers when application transport is installed.

Focused notification verification:

```sh
go test ./internal/board -run 'TestRelayMentions|TestUserMentions' -count=1
go test ./internal/relayhost -run 'TestNotifications|TestNotification|TestRevocationWaits' -count=1
go test ./internal/server -run '^TestRelayNotifications' -count=1
```

The server tests use the real relay HTTP route and the production startup
callback with durable active-device fixtures and no UI socket. Host tests
exercise receipt persistence/restart, lost replies, source-read interruption
after acknowledgment commit, concurrent polls and cancellation at the source
read boundary. Existing host writer-join coverage checks revocation while an
admitted response write is unfinished.
