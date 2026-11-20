# Outbound relay host

omo can keep one outbound encrypted relay connection, preserving its host key,
routing name and paired devices across restarts. It opens no additional inbound
port and needs no host TLS certificate. This page describes host commands and
private state. The native UI and the application/notification consumers are
separate integrations; absent consumer callbacks grant no application access.
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
Install `relayhost.Callbacks` with `Manager.SetCallbacks`:

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
