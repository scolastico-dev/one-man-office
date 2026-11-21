# Relay protocol version 1

This is the normative endpoint and interoperability contract for version 1. The
`internal/relayproto` package implements the wire/crypto primitives. Routing,
host persistence, application serving and UI policy are separate consumers;
this document specifies their required integration behavior, not their current
availability. The relay routes opaque messages between an outbound omo host
and several native UI clients. Trust, keys, authorization and notification
receipt state belong to the endpoints.

The host authorizes a device only through the real host's local PIN or full-key
accept command. Successful local acceptance stores the device as active before
CLI success and encrypted `pair.accepted`. There is no extra UI confirmation
or `pair.confirm` activation step. No UI is implemented here.

## 1 Existing integration points

Integration boundaries:

* `go.mod`: Go 1.26.2, pure Go, existing `github.com/coder/websocket v1.8.15` and Cobra. No new cryptographic dependency is necessary.
* `internal/server/auth.go`: `BrowserHandler` applies browser Host/Origin/login checks; those checks remain on TCP. `internal/server/socket.go`: `SocketHandler` resolves `X-Omo-Session`, and absence of the header means the local user. It is **not** a remote transport handler.
* `internal/server/access.go`: existing route access rules and board authorization remain authoritative. Paired devices have the human user's authority, including terminals and settings, rather than agent authority.
* `internal/server/events.go`: `/api/events` is streaming SSE, supports `Last-Event-ID` or `since` and explicit resync. `internal/webterm/websocket.go` and server terminal handlers use real HTTP upgrades, binary/text WebSocket frames and the `omo` subprotocol.
* `internal/apiclient/inprocess.go`: `HandlerTransport` buffers a complete response and has a no-op Flush; it cannot carry SSE or WebSocket upgrades. Do not use it as the application tunnel.
* `internal/board/thread.go`: `UserMentions` returns newest-first pages of 50 with a global `mentions_seen` marker. Transport receipt must be an independent per-device cursor and must not call `SetMentionsSeen`.
* `internal/serverconfig/config.go`: strict YAML plus explicit Defaults/Keys/Validate/Set/Unset paths. `internal/globalhome/home.go`, `atomic.go`, `internal/homefile/homefile.go`, `guard_test.go`, `internal/homezip/allowlist.go`: private state, bounded safe reads, atomic replacement and home classification all need explicit integration.
* Preserve `internal/proxy`, `/proxy/`, `omo ai proxy` and `internal/mcprelay` completely. Use `relayproto`, `relayserver`, `relayhost` and `relaytransport` for this feature.

## 2 Encoding and routing

### Common representation

Endpoints support exactly `v=1`, suite `MLKEM768-HKDF384-AES256GCM`. Reject another version/suite; no downgrade. One WebSocket message is one UTF-8 JSON object. Reject duplicate/unknown keys, invalid UTF-8, noncanonical base64url and out-of-range values. Every control, handshake, record and encrypted control/stream object carries `v,type`; the notification request, envelope and page have their explicit schemas below; the suite is fixed by v and never negotiated. Field tables omit those common fields. Binary strings use unpadded RFC 4648 base64url. All counters, comment/ticket IDs and stream IDs are canonical unsigned decimal **strings** (avoid JavaScript integer precision). All HTTP paths are case-sensitive. Decimal strings apply to protocol counters/stream IDs and notification comment/ticket IDs only. Existing tunneled HTTP bodies retain their current encodings (including numeric API IDs); application JSON is opaque and is never rewritten.

Cryptographic serialization never hashes JSON. Define `E(x1,...,xn)` as concatenated `u32be(byte-length(xi)) || xi`. Text is exact UTF-8; numeric fields in E are fixed-width unsigned big-endian (v:16 bits; counters:64 bits; PIN expansion index:32 bits). Fields have the order shown in each formula; labels are literal ASCII with prefix `omo-relay-v1/`. `H` is SHA-384 (48 bytes), `MAC` is full HMAC-SHA-384, `KDF(ikm,salt,label,n)` is HKDF-SHA-384 Extract then Expand with info `E("omo-relay-v1/"+label)`. Compare commitments/MACs/tags in constant time.

`host_id` is a routing name, `[A-Za-z0-9_-]{1,64}`. Generate 16 random bytes as base64url when no id was selected; persist it before first registration, so a normal restart uses the same id. `host_fp=hex(H(E("omo-relay-v1/host-key",pkH)))`; `ui_id=hex(H(E("omo-relay-v1/ui-key",pkU)))`, lowercase 96 hex characters. They identify keys; neither is a bearer credential. The relay address R is the canonical configured `https://lowercase-host[:nondefault-port]`, with no credentials/path/query/fragment; WebSocket uses the corresponding `wss://` origin. Both endpoints bind R and host_id to their cryptographic contexts. A different relay/name requires explicit local configuration on each endpoint and fresh authentication, never silent key replacement.

### Relay admission and control plane

Relay exposes `GET /v1/ws` and `GET /notifications/<host_id>/<ui_id>`. Optional admission password is HTTP Basic username `relay` and the configured password, checked before WebSocket upgrade or notification forwarding. All clients need it when enabled. It is separate from host/device authentication; it grants no omo access. No password in URLs, echoed errors, content logs or forwarded headers. Native desktop/Android clients can set handshake headers. Use WSS/HTTPS with ordinary certificate verification; explicitly allowed loopback WS/HTTP is only for local development. TLS may terminate in a reverse proxy at the relay; the host opens no new TCP port and needs no inbound certificate.

Control objects have `v,type` plus the listed fields. Registration, connection, qid, aid, window and epoch values decode to their stated fixed byte sizes; callers cannot choose relay-generated routing handles. Names below are literal message types:

| Direction | Message and fields | Result |
| --- | --- | --- |
| host to relay | `host.register {host_id}` | First live connection wins atomically. |
| relay to host | `host.registered {host_id,registration}` | registration is relay-generated random 16 bytes, scoped to that socket. |
| relay to host | `error {code:"id_in_use"}` | Close the losing registration; never replace the existing host. |
| UI to relay | `ui.attach {host_id}` | Only after admission; host must be live. |
| relay to UI | `ui.attached {connection}` | New random 16-byte connection routing handle. |
| relay to host | `ui.open {connection}` | Creates an unauthenticated endpoint channel only. |
| either endpoint | `forward {connection,payload}` | payload is base64url of one inner JSON object; relay treats its bytes as opaque. UI connection is fixed by attach; reject a different handle. |
| relay to host | `ui.closed {connection}` | Close that endpoint session and its streams. |
| host to relay | `ui.close {connection}` | Relay closes only that UI. |
| relay to UI | `error {code:"host_offline"}` | Close; no attach queue or stored content. |

Host registration is the first control message; UI attach is the first UI control message. No role changes on a socket. The host waits after registration, without creating an agent or application stream. It may receive many independent `ui.open`s; each starts unauthenticated. Responses from a host must match that host socket's registration and connection map, not just a host_id string. Keep that scoping on asynchronous callbacks too.

Host socket loss atomically removes its registration, cancels notification requests and closes all its UI sockets. A late callback from the old host cannot remove or answer for its replacement. Relay restart clears every map. Host automatically redials its saved id. On `id_in_use`, show status `collision` and keep retrying while relay_enabled is true, with delays 1, 2, 4, 8, 16, then 30 seconds capped for every further attempt; reset backoff only after successful registration. Never take over a registration or silently select another name. This handles both selected and random ids. A genuine foreign collision remains visibly retrying until its owner leaves or the human changes the id; local `connect --new-id` explicitly generates a replacement. Lost application sessions always redo authentication with fresh randomness/keys; do not replay uncertain writes. Independent UIs remain connected when a different UI leaves.

The host bounds dial/upgrade and the initial wait for host.registered to 30 seconds each. A silent relay or middlebox closes that attempt and enters the same saved-id reconnect loop; keepalive begins after registration.

Host-local admission is capped at 128 endpoint channels and 128 pending notification polls per registration. Refuse excess/duplicate endpoints individually with ui.close and excess/duplicate/misdirected polls with notification.empty. Duplicate handles cancel only that endpoint or poll, never the shared socket. Later frames for a refused/closed endpoint have no authority and are ignored. Refusal replies are best effort through bounded queues: saturation may drop that one close/empty reply, with no growing refusal backlog, while the control reader and other authenticated devices remain live. The relay's ordinary GET timeout covers a dropped empty response.

Keepalive is mandatory: the relay sends a WebSocket Ping to every registered host and attached UI every 20 seconds, first at 20 seconds after registration/attachment. Each Ping must receive its matching Pong within 10 seconds; on expiry, immediately close that socket and remove its scoped routing registration/connection, cancel its pending GETs and close affected UI sessions as described above. The host independently Pings its relay socket on the same 20-second interval/10-second deadline, closes and redials on failure. Both endpoints continuously service the WebSocket read/control loop, even with no application traffic or a busy request handler; other traffic does not satisfy a missing Pong. Thus a registration whose socket becomes half-open is reaped no later than the next Ping deadline (at most 30 seconds after its last working control exchange), and the enabled host eventually reacquires its saved id through collision retries. No resume token or registration takeover is used. Use `Conn.Ping` concurrently with the existing continuous read loop, and `CloseNow` on timeout so removal does not wait for a close handshake. [WebSocket Ping/read API](https://pkg.go.dev/github.com/coder/websocket@v1.8.15#Conn.Ping). Timeout tests inject a clock or use synctest and wait for exact expiry/removal/retry events; real time is not a test pass condition.

Use bounded in-memory routing queues and one writer per socket. Outer messages are at most 1 MiB, forwarded inner messages at most 64 KiB, encrypted stream data at most 16 KiB per chunk, notification replies at most 512 KiB. Backpressure closes a slow endpoint instead of buffering content indefinitely. Mandatory keepalive never resets pairing permission. No relay persistence, disk spool, notification cache or payload logging. Routing names and traffic sizes/timing remain visible; a malicious relay can deny service or occupy a name.

## 3 Keys and authentication

The version 1 primitive suite is ML-KEM identities with mutual MAC possession proofs, staying within Go's standard crypto library and retaining a separate shared root for offline notifications. Adding post-quantum signatures would instead require a second identity/signing format and key lifecycle; signatures are unnecessary when both endpoints already hold paired trust and a notification root. Relay-terminated TLS alone cannot meet the compromised-relay threat model. These choices define the v1 primitives; first-contact limits are described in section 4.

ML-KEM-768 generates an encapsulation public key (1184 bytes), encapsulation ciphertext (1088 bytes) and shared secret (32 bytes). Use Go `crypto/mlkem.GenerateKey768`, `NewEncapsulationKey768`, `Encapsulate`, `Decapsulate` and `NewDecapsulationKey768`. Go private serialization is a **64-byte d||z seed**, not an expanded ML-KEM private key. Keep it local. Raw FIPS 203 public-key/ciphertext bytes are the interoperable wire format, with explicit suite and lengths; no PEM/ASN.1/Go object serialization or prestandard Kyber encoding. [Go 1.26.2 ML-KEM API](https://pkg.go.dev/crypto/mlkem@go1.26.2).

ML-KEM establishes a secret; it is not a signature and merely sending a public key or ciphertext proves no identity. Version 1 uses two independent KEM challenges plus transcript-bound MAC confirmations to prove possession of the **pinned** private keys. It needs no RSA or signature algorithm. Initial pins come only from the pairing ceremony, never from relay TLS or the routing id. NIST standardizes the primitive and its use constraints; it does not standardize or certify this application protocol. [FIPS 203](https://csrc.nist.gov/pubs/fips/203/final), [KEM guidance](https://csrc.nist.gov/pubs/sp/800/227/final).

Later UI implementations can consume these raw standard encodings with a compatible library, or share this Go core via native bindings. For Java, Bouncy Castle provides ML-KEM public-key decoding, encapsulation and extraction; select `ml_kem_768` rather than legacy Kyber. Cross-language fixtures belong to the protocol task; no Android/desktop implementation is in this story. [Public-key API](https://downloads.bouncycastle.org/java/docs/bcprov-jdk18on-javadoc/org/bouncycastle/crypto/params/MLKEMPublicKeyParameters.html), [generator](https://downloads.bouncycastle.org/java/docs/bcprov-jdk18on-javadoc/org/bouncycastle/crypto/kems/MLKEMGenerator.html), [extractor](https://downloads.bouncycastle.org/java/docs/bcprov-jdk18on-javadoc/org/bouncycastle/crypto/kems/MLKEMExtractor.html).

### Mutual possession handshake

Run this after pairing reveal, or immediately for reconnect. All messages are inner payloads. Prefix p is `pair.auth` during pairing and `auth` on reconnect. Context `B=E("omo-relay-v1/auth-context",v,suite,mode,R,host_id,host_fp,ui_id,pkH,pkU,pair_epoch,pair_hash)`; mode is literal `pair` or `reconnect`; pair_hash is the pairing transcript hash below (empty on reconnect). pair_epoch is a host-generated random 16-byte pairing generation, persisted in the device record. UI gets it in `pair.host`; on reconnect it sends its saved value. Both endpoints reject a different key/epoch rather than updating a record.

1. U encapsulates to pinned/candidate pkH: `(ssH,ctH)`. It sends `p.init {host_fp,ui_id,pair_epoch,nu,ctH}`, nu fresh random 32 bytes. In pairing pkU is already fixed by reveal; on reconnect look it up by ui_id and epoch. No public key supplied in reconnect can override it.
2. H decapsulates ctH, generates random nh (32 bytes), encapsulates to pinned/candidate pkU: `(ssU,ctU)`. Define `T=H(E("omo-relay-v1/auth-transcript",B,nu,nh,ctH,ctU))`, `PRK=HKDF-Extract-SHA384(salt=T,IKM=ssH||ssU)`. H sends `p.challenge {nh,ctU,proofH}`, where `proofH=MAC(Expand(PRK,E("omo-relay-v1/finish-host"),32),E("omo-relay-v1/host-finished",T))`.
3. U decapsulates ctU, derives PRK, verifies proofH and sends `p.finish {proofU}` with separate key `Expand(PRK,E("omo-relay-v1/finish-ui"),32)` and input `E("omo-relay-v1/ui-finished",T,proofH)`. H verifies before granting any device authority. Each side's own encapsulation is fresh. An impostor knowing its own encapsulated secret still lacks the other challenge's secret. No decapsulation result, raw secret, PIN or PIN-check result is sent.
4. On reconnect H rechecks current device authorization and sends encrypted `ready {pair_epoch,state,aid}`, where state is `active` and aid is the saved pairing attempt id. U responds encrypted `ready.ack {pair_epoch}`. Streams may then open. Session id is base64url(T). A handshake is attached to one connection, one init and one challenge; another/out-of-order message aborts it. Duplicate or tampered proofs fail closed with a generic authentication failure. No application data is accepted during authentication.

Here Expand is RFC 5869 HKDF-Expand-SHA384 with the literal info bytes supplied. Separate Expand info values `E("omo-relay-v1/record-ui-host")` and `E("omo-relay-v1/record-host-ui")` derive AES keys `record-ui-host` and `record-host-ui` (32 bytes each) from PRK. This is fresh key establishment through the post-quantum identities, not reuse of the identity key as AES. Hashes bind mode, identities, roles, route, version, epoch and the full exchange. Erase ephemeral secrets/session keys on close. Static KEM identity compromise later can expose recorded sessions; version 1 does not claim forward secrecy against endpoint key theft. It protects against the relay, including recorded traffic under a future quantum attack under the ML-KEM assumption.

## 4 Pairing ceremony and attempt budget

The host normally rejects pairing. The state machine below consumes a single locally authorized budget; `PairHost.Accept` returns acceptance material; the host consumer must persist its device record as active before acknowledging the local command. The human runs `omo relay pair begin`; this durably opens exactly one local slot with random window (16 bytes) for at most five minutes. There is only one slot for the entire host, across UI sockets and relay reconnects. Local state is `closed -> armed -> committed -> revealed -> proved -> accepted`, or `spent`. No remote event can return it to armed.

```text
UI                    untrusted relay                    host
pair.commit aid,C  ------------------------------------> freeze first commit
                   <------------------------------------ pair.host pkH,rh,window,epoch
pair.reveal pkU,ru  ------------------------------------> verify C, freeze identity
mutual KEM init / challenge / finish -------------------- prove both candidate keys
display PIN                                               await local command
human reads UI PIN ---------------- local keyboard -----> accept aid PIN (or full pkU)
                   <------------------------------------ encrypted pair.accepted
                                                         device is active after local save
```

UI generates a persistent ML-KEM identity if needed, random aid (16 bytes) and ru (32 bytes) for this ceremony. It persists its tentative attempt before sending, and sends **only** `pair.commit {aid,C}` where `C=H(E("omo-relay-v1/pair-commit",v,suite,R,host_id,aid,pkU,ru))`. It sends neither pkU nor ru, ui_id nor a key-dependent descriptive name at this step.

Host atomically records the first commit, connection, aid and C as consuming the slot **before** answering. It returns `pair.host {aid,window,pair_epoch,pkH,rh}`: persistent host key pkH, independent fresh rh (32 bytes) and pair_epoch. This is the host's fixed contribution before UI reveal, not a second hash message. UI checks the matching aid and freezes the first such response before sending `pair.reveal {aid,pkU,ru}`. Neither endpoint accepts changed contributions, a second reveal, or a new peer under the same slot. Host validates C and the public key. Both compute:

`P=E("omo-relay-v1/pair-transcript",v,suite,R,host_id,aid,window,pair_epoch,C,pkU,ru,pkH,rh)`; `pair_hash=H(P)`.

PIN is exactly six decimal digits, leading zeros retained. For i=0,1,..., read the first u32be of `H(E("omo-relay-v1/pin",P,u32be(i)))`; take the first value less than 4,294,000,000, reduce modulo 1,000,000, zero-pad. The keys and random values in P are fixed **before** either side can know the other side's full PIN input. Do not include later KEM ciphertexts, names or acceptance messages in the PIN, since those would allow grinding after reveal. Display PIN only after commitment verification and mutual possession proof. The PIN is human comparison data, not secret key material.

`omo relay pair accept <aid> <PIN> --name <label>` compares against this one immutable proved slot. `--public-key <base64url-1184-bytes>` instead of PIN compares the entire pkU supplied from the UI over the human channel; a fingerprint or truncated key does not satisfy this option. Names are local labels, never authority. A wrong PIN/key, invalid reveal/proof, timeout, cancellation, UI/relay disconnect or host restart burns the slot. An incorrect local acceptance also burns it. No retry button, duplicate commit, second socket or timer gets another challenge in that ceremony. Persist consumed state before transmitting and treat any unfinished slot on startup as spent. Require a new explicit `pair begin` and a new UI ceremony, visibly warning that it is a new attempt. A slot cannot be accepted after its connection was lost.

This bounds an active relay to one independent six-digit substitution trial per human-authorized ceremony (approximately 1/1,000,000, rather than unlimited adaptive trials). It can steal the first slot and cause failure/denial of service. It cannot silently cycle slots, compare a typed PIN against a list of pending attempts or grind a late host random against a reused honest UI reveal. The UI must likewise refuse replaying/revealing its old commitment into another response or auto-starting a new ceremony. Explicit new human ceremonies accumulate their usual guessing probability. Commitment/SAS is an established technique, but this exact construction remains a proposed protocol. [ZRTP commitment and SAS rationale](https://www.rfc-editor.org/rfc/rfc6189#section-4.3.2).

### Trust persistence and first contact

After mutual proofs, both derive notification root `N=Expand(PRK,E("omo-relay-v1/notification-root",pair_hash),32)`. The UI keeps its host candidate, pairing epoch/root and identity in private persistent storage so the exact ceremony can recover a lost reply.

On successful local PIN or full-public-key acceptance, H durably stores pkU, ui_id, pair_epoch, N, original aid, local label and state `active`. Only after this save does it return local CLI success (printing aid and host_fp) and send encrypted `pair.accepted {aid,host_fp,ui_id,pair_epoch}`. This local acceptance is the authorization point for application sessions and authenticated notification GETs. No network message or timer authorizes an unregistered device. There is no awaiting-confirmation state and no pair.confirm exchange.

If the accepted reply is lost or the host/UI/relay restarts, the same saved key and epoch may reconnect with fresh mutual proof, receive active ready and send ready.ack. Never regenerate the saved notification root or silently replace either pinned key. An unaccepted or spent attempt cannot reconnect as an authorized device. Losing tentative UI secrets requires unregistering any accepted registration and starting a new local ceremony.

The PIN bounds substitution against the real host to one trial per local ceremony, and full-key acceptance additionally compares the complete candidate UI key with private possession proof. First contact still has an identity limit: cryptography proves possession of the candidate host key, and a fake host can send an accepted-looking response under its own key. A routing name, relay TLS or a network success alone is not independent evidence that this candidate is the user's intended host. The selected product flow uses the local host command plus PIN/full key without adding an out-of-band fingerprint invitation or a second confirmation step. The later native UI must visibly distinguish its pairing/connection screen, including titlebar/chrome, from forwarded host pages; forwarded content must not masquerade as that native screen. This task supplies no UI implementation.

## 5 Encrypted records and application transport

Encrypted inner object is `record {session,seq,ciphertext}`. `seq` starts at zero independently in each direction; nonce is `0x00000000 || u64be(seq)`, 12 bytes. AES-256-GCM uses a 16-byte tag; ciphertext is sealed bytes including the tag. AAD is `E("omo-relay-v1/record",v,suite,T,direction,u64be(seq))` where direction is `ui-host` or `host-ui`. Use `aes.NewCipher(32-byte-key)` and `cipher.NewGCM`, never optional tag/nonce lengths. Increment a single direction counter in the session writer for **all** control and stream records, not per stream. [Go GCM API](https://pkg.go.dev/crypto/cipher@go1.26.2#NewGCM), [HKDF](https://www.rfc-editor.org/rfc/rfc5869).

Authenticate before parsing plaintext or changing receive state. Require seq to equal the next expected value, so duplicate, gap, reflection or reordering aborts that session. Endpoints allow at most 2^24 records and 2^36 plaintext bytes per direction, redoing the handshake before the next record would exceed either bound; drain/cancel streams and establish a new session instead of resetting a nonce counter. No session/counters survive a socket loss. The relay cannot splice another session or device's ciphertext undetectably. GCM does not prevent deletion, delay or denial of service.

Record plaintext is strict JSON with `v,type` and the following fields. Handshake ready/confirm records above use the same sequence space. Application data becomes a multiplexed byte transport, allowing the existing HTTP and WebSocket protocols to operate unchanged inside encryption:

| Type | Fields and semantics |
| --- | --- |
| `stream.open` | UI sends fresh odd stream id, starting `"1"`, increasing by 2, never reused per session. One HTTP/1.1 connection per stream. |
| `stream.opened` | H acknowledges id only after current device authorization and stream allocation. Both directions now have 65,536 bytes initial receive credit. |
| `stream.data` | `{id,data}`; data is base64url, 1..16,384 decoded bytes; consumes the peer-granted byte credit. Ordered bytes within the stream. |
| `stream.credit` | `{id,bytes}` grants bytes actually drained by the consumer, at most restoring 65,536 outstanding credit. An overflow/oversend aborts session. Control records need no data credit. |
| `stream.fin` | `{id}` is ordered write EOF after preceding data; opposite direction may continue. No later data in that direction. |
| `stream.reset` | `{id,code}` cancels both directions and releases resources; only fixed codes, no reflected sensitive text. |

For an allocated/opened stream, each direction sends at most one `stream.reset`.
The receiver stops enqueueing data/credit/FIN, discards buffered payloads, and
reciprocates with `stream.reset {id,code:"closed"}` unless its own reset is
already queued/sent. These reciprocal resets acknowledge cancellation without
a new message schema. Until both its reset has been serialized and the peer
reset received, an endpoint retains only bounded credit/direction/FIN state
and validates/discards traffic that crossed its reset in flight. Oversend,
credit overflow and data after that direction's FIN remain protocol errors.
No further traffic may follow an endpoint's reset for that stream. The ordered
peer reset proves its previous traffic has drained; then the slot is released.
A refusal before `stream.opened` allocates no credit and needs no reciprocal
reset. Never evict a still-valid reset snapshot by age/FIFO: resetting streams
count toward the 64 slots, bounding state while preserving unrelated streams.

Cap at 64 simultaneous streams per authenticated session. A fair writer handles ready streams and controls without letting a full SSE/terminal queue block another stream or another UI. Streams have bounded receive queues; readers grant credit only on consumption. Unknown ids, repeated opens, premature data and invalid state transitions are protocol errors. Session closure cancels all stream contexts and waits for their goroutines to exit.

The host supplies virtual `net.Conn`s from those streams to a private in-memory `net.Listener`, served by an HTTP/1.1-only `http.Server` with `RelayHandler` and connection-bound device capability. It binds no OS port and dials no untrusted destination. Use real `net/http` parsing, flushing and Hijacker; do not invent a buffered ResponseRecorder. This supports raw/large request bodies, ordinary JSON responses, streamed responses, SSE flushes and terminal WebSocket upgrades, text/binary messages, control frames and close codes. UI sends one HTTP request per non-upgraded stream (`Connection: close`, no pipelining); keep the stream until response EOF. WebSocket streams remain bidirectional until close. [Go Server.Serve and Hijacker](https://pkg.go.dev/net/http@go1.26.2#Server.Serve), [existing WebSocket implementation](https://pkg.go.dev/github.com/coder/websocket@v1.8.15).

Later UI must send **all host loads** over these streams: APIs, images/uploads, logs, assets, events and terminals. It cannot fall back to directly opening the host's HTTP address. Reject CONNECT, absolute-form request targets, authority-form targets, invalid/ambiguous escaped paths and requests naming another authority; accept only origin-form paths and the normal application methods. Serve canonical Host `omo-relay.invalid` and absent Origin or `http://omo-relay.invalid` inside the tunnel, including upgrade requests so existing WebSocket Origin validation works. SSE reconnect uses the existing Last-Event-ID/resync contract in a new stream. Do not automatically replay POST/PUT/DELETE or terminal input after uncertain delivery. Existing terminal replay/message-ack behavior remains inside its WebSocket.

### Explicit authorization boundary

`RelayHandler` accepts only a server-created capability from the authenticated connection context: ui_id, pair_epoch, authorization generation and cancellation context. Require active device and matching generation before every request/stream; missing capability is 401. It sets `board.User` explicitly from this verified device, applies JSON/upload requirements and existing route/board permission rules, then serves the normal application mux. It never calls `SocketHandler` or authenticates with an arbitrary forwarded header. Agent/session/terminal ids in payloads still undergo their existing resource checks.

Reject requests containing `X-Omo-Session` (including empty, mixed-case or multiple occurrences), every other `X-Omo-*` request header, Proxy-Authorization, relay admission credentials, or omo login-token WebSocket subprotocols. Remove client Authorization/Cookie and forwarded identity/proxy headers before application dispatch; no fallback to local missing-header trust. Expose no local socket, `/mcp`/OAuth/identity-admission endpoints or `/api/relay/*` pairing/device administration. Do permit the actual board application routes, assets, image endpoints and terminal upgrades. `/proxy/` and proxy subdomain routing remain on their existing browser path; creating/listing forwards through normal API may work, but no new relay support or changes to that feature are included.

Revocation persists removal/epoch invalidation, invalidates capabilities and cancels every live session, virtual connection, SSE subscription, terminal viewer and pending notification response for that ui_id before unregister returns. It does not kill underlying host shells or agents merely because their viewer left. Serialize authorization checks/admission with revocation: no new operation can be admitted after its revocation point. Work already committed cannot be undone; cancellation stops uncommitted/streaming work using existing context semantics. Other devices remain active.

## 6 Notifications without a live UI WebSocket

GET is immediate request/response through the host's existing relay socket; the UI needs no WebSocket or session key. It does need the paired root N, identity pins and active pair_epoch saved across restarts. Derive `NK=KDF(N,H(E("omo-relay-v1/notify-context",R,host_id,host_fp,ui_id,pair_epoch)),"notify-request",32)`.

UI sends `GET /notifications/<host_id>/<ui_id>` with optional relay admission Authorization and two headers:

* `Omo-Notification-Request`: base64url of strict JSON `{v,pair_epoch,request,ack}`. request is fresh random 32 bytes per outstanding poll. ack is null, or `{batch,through,token}` from a previously decrypted and durably stored batch; batch is 16 random bytes, through is a decimal comment id, token is 32 random bytes.
* `Omo-Notification-MAC`: base64url `MAC(NK,E("omo-relay-v1/notify-get",v,R,host_id,host_fp,ui_id,pair_epoch,request,batch-or-empty,through-u64-or-empty,token-or-empty))`.

Relay checks admission and header/size syntax only. It allocates random qid (16 bytes), maps `(registration,qid)` to this pending HTTP response, and sends host `notification.get {qid,host_id,ui_id,request_header,request_mac}`. The inner header/MAC bytes are forwarded unchanged; relay Basic credentials are not. Host verifies active epoch and MAC before querying/acknowledging. Host replies `notification.reply {qid,body}` where body is the opaque encrypted response envelope below, or `notification.empty {qid}` for an unknown/revoked/invalid device. Relay accepts exactly one reply only from that registration and qid, removes the pending map entry, and writes it to that request. Late/foreign/duplicate replies are discarded. HTTP client cancellation sends `notification.cancel {qid}`; host cancels work. Request deadline (30 seconds), host departure and relay shutdown remove outstanding entries; no offline queue.

Live valid request returns HTTP 200, `application/vnd.omo.relay-notifications+json`, with strict JSON `{v,host_fp,ui_id,pair_epoch,request,reply,ciphertext}`. reply is independently random 32 bytes for **every** response, including repeated request ids. Define header bytes `A=E("omo-relay-v1/notify-response",v,R,host_id,host_fp,ui_id,pair_epoch,request,reply)`, `K=KDF(N,H(A),"notify-response",32)`. Seal one plaintext with AES-256-GCM using nonce twelve zero bytes, AAD=A. Each response gets a fresh key from reply randomness, including after host restart; no key/counter reuse from the live record layer. This persistent root authenticates the host response: unauthenticated encryption solely to a public UI KEM key would let anyone forge notification content. request is checked against an outstanding UI poll, preventing a captured old response from being accepted as a new check.

Plaintext is `{batch,through,token,notifications,more,acknowledged}`. Empty online result is authenticated/encrypted with `notifications:[]`, batch/token null, current cursor through and more false. acknowledged is the batch id just accepted or null. A nonempty result is the next oldest page for that device (at most 50 entries and 256 KiB plaintext, including envelope fields); `more` says whether another page exists. Use existing mention qualification, ordered by increasing comment id; do not filter on global seen. Each notification is a bounded projection `{id,ticket,author_role,created_at,title,body,body_truncated,title_truncated,unavailable:false}`. IDs are decimal strings, author_role is the existing role enum, created_at is UTC RFC3339, title is at most 128 UTF-8 bytes and body is at most 384 UTF-8 bytes, truncated only at a code-point boundary with the respective flag. No arbitrary author labels or mention arrays are included. Encoded notification JSON must be at most 4096 bytes, including worst-case JSON escaping; a full page of 50 therefore fits the 256 KiB reply budget even if source text changes on retry. Fetch the full comment through the encrypted application transport. A single oversized comment can never block later notifications; the notification preview itself is the item being delivered. New device cursor starts at zero, so retained older mentions are delivered too. through is the last included id, never an id of a hidden/unreturned qualifying notification.

Keep **one** durable pending batch per device: batch id, ordered notification ids, through and a random secret receipt token. Save before sending a body. Concurrent checks/retries return that same batch id/ids/through/token (fresh response encryption and current bounded previews), until acknowledged. Query source records again to generate content; if a source record disappeared, include its id with `unavailable:true` so the page is still complete. Do not copy notification content into relay storage. The host may retain the small receipt metadata, its source is already durable omo data.

Sending, forwarding, reading an HTTP response, timeout or a relay's success claim never advances the received cursor. UI first decrypts, authenticates and durably records/deduplicates **all** entries, then includes the exact receipt in its next authenticated GET. Host serializes poll/ack handling per device. Alongside its cursor and at most one pending batch, persist `last_ack` (null initially, otherwise the exact `{batch,through,token}` of the most recently acknowledged batch). Apply these mutually exclusive transitions after active-epoch/MAC validation:

| Poll ack | Atomic host action | Authenticated response |
| --- | --- | --- |
| null | Leave cursor, last_ack and pending unchanged; generate/persist the next pending batch if needed. | Current pending batch or empty; `acknowledged:null`. |
| Exactly current pending tuple | In one save, advance cursor to pending.through, set last_ack to that tuple and clear pending. Then generate/persist the next pending batch if any. | Next/current pending batch or empty; `acknowledged:<accepted batch id>`. |
| Exactly last_ack tuple | Do not change cursor/last_ack or clear a newer pending batch; generate next pending if needed. | Current pending batch or empty; `acknowledged:<last_ack batch id>`. |
| Anything else, including an older tuple or a forged token with through <= cursor | Do not change cursor/last_ack/pending. | Authenticated plaintext `{error:"invalid_ack"}` inside the normal encrypted envelope. |

Comparisons require the complete tuple, including constant-time token comparison; a low through alone is never accepted. Recheck active epoch after reading the board and before persisting a newly prepared batch or sending its response. Never hold the global state-store mutex across a board query or network write. Cursor and last_ack are saved together before any successful ack response: a crash after that save or loss of the next-page reply is recoverable by the exact last_ack branch after restart. Replay of ack X while Y is pending returns `acknowledged:X` plus Y, without consuming Y. Losing a reply before a new batch was persisted is also safe: the retry generates it from the already committed cursor.

UI maintains one durable polling/receipt chain per identity and one logical outstanding poll; retries use fresh request ids and abandon the superseded id so delayed responses cannot overwrite newer receipt state. Host still tolerates concurrent duplicate requests. With no outstanding receipt, a valid current response simply installs the returned pending tuple after durable notification storage. After validating a response for the current request, the UI may clear its outstanding receipt only if `acknowledged` equals that receipt's batch id. Atomically record/deduplicate the returned next batch and replace the receipt with its tuple (or null for empty). If the reply is lost, undecipherable, an untrusted HTTP empty/error, or does not acknowledge that tuple, retain the receipt and retry it. A valid authenticated `invalid_ack` is the explicit recovery exception: discard only the stale receipt, retain all locally received notification data/ids, and poll again with ack null; the current host pending batch is returned, deduplicated and acknowledged normally. This also recovers an out-of-order receipt older than last_ack without suppressing notifications.

The receipt for device A cannot acknowledge B. Root possession alone cannot guess a future receipt; arbitrary callers/relay cannot generate request MACs, decrypt a receipt token or acknowledge notifications that the device has not received. Replaying an already valid ack acknowledges only a batch already received by that device. No exactly-once network promise: at-least-once until durable acknowledgment, UI deduplication by comment id.

HTTP 204 with a zero-byte body covers host offline, unavailable/unknown device or invalid device authentication. Host timeout also returns 204. No notifications are consumed. Admission failure is 401 (with Basic challenge) and malformed path/header is 400; outer overload may be 503. These empty/error statuses are untrusted availability hints, **never authenticated evidence that the host has no notifications**. Only a successfully decrypted 200 with an empty list proves an online empty answer. All answers have `Cache-Control: no-store`; relay does not cache/log bodies. A malicious relay may suppress replies or cause repeated polls but cannot consume notifications without genuine device acknowledgment.

Normal host/relay/UI restart preserves cursors and unacknowledged receipts. Database/home backup rollback is not a normal restart: delivery state must be reset explicitly if restoring an older independent database, because comment ids may have been reused. This feature does not introduce a backup migration system.

## 7 Consumer human controls and persistent state

Consumer contract for human Cobra commands (not implemented by relayproto), all using only local user-authorized control routes:

```text
omo relay connect https://relay.example --client-id office [--password-stdin]
omo relay connect https://relay.example --new-id [--password-stdin]
omo relay status
omo relay disconnect
omo relay pair begin
omo relay pair pending
omo relay pair accept <aid> <six-digit-PIN> --name phone
omo relay pair accept <aid> --public-key <full-base64url-key> --name tablet
omo relay pair cancel
omo relay devices
omo relay unregister <ui-id>
```

`connect` without either id option reuses saved id, or generates one if absent. Persist desired enabled/disabled state; restart reconnects only when enabled. `disconnect` stops outbound connections and burns a pending attempt, retaining device registrations for later. `status` distinguishes connected, retrying, admission denied and collision, prints route and host fingerprint, never secrets. `pending` shows at most one aid and key fingerprint/state, not a candidate list to search by PIN. `devices` prints exact ui_id, local name, host acceptance time, active and live connection count. `unregister` works offline too, persists revocation and prevents reconnect, notifications and live access; re-pair requires new local acceptance/epoch. Control routes `/api/relay/*` require **local socket human context**, not just user role; agent sessions, browser TCP and paired remote devices cannot self-register devices or cancel revocation.

Add strict `server.yaml` keys `relay_url` (empty disables configuration) and `relay_enabled` (false default), including all config setter/validator paths. Password, selected/generated id and trust material live at `<home>/relay/state.json`; do not put an admission password into exported server.yaml. `connect` updates private state before enabling and signals the running manager; disabled config does not delete state. The private state also stores its bound origin R. A config/state origin mismatch pauses access and requires explicit connect to reconcile; it cannot combine an old password/trust context with a new server silently. A changed relay origin is explicit and pauses access until clients are reconfigured for it.

State version 1 stores host seed/public fingerprint, route origin R and id, relay admission secret, pair slot consumption marker, active devices (pkU, epoch, N, label), original pairing aid, notification cursors, last_ack (null or exact batch/through/token tuple) and pending receipt metadata. Cursor and last_ack commit together; normal restart preserves cursor, last_ack and pending. Single server-owned store with a mutex serializes read-modify-atomic-write; CLI never edits it concurrently with the server. Seed generation and trusted-device updates must commit before success is reported. `relay/` is a private plain directory, state file 0600/protected Windows ACL, safe reads via `homefile.Read` with explicit `MaxRelayState` (4 MiB), writes via `globalhome.WriteFileAtomic`, and no secret logging. Add `Layout.Relay`, classify `relay` in `homezip.NotExported` with identity/private admission/delivery-state reason, and add relayhost to homefile's reader guard. Check parent directory is a plain private directory too; homefile protects the last element only. Fail closed on corruption, invalid key/duplicate ui_id/unsupported state version or failed save, never silently generate replacement identity.

Local state is not in normal settings/database exports; a whole-home secure backup can preserve it. Importing server.yaml without this private state does not enable an unknown identity automatically. Rolling back a whole-home backup may restore a revoked registration; backup rollback is an explicit administrative trust rollback, not a relay restart guarantee. UI stores its seed, trusted/tentative host pins, pairing epoch/root, received notification ids and outstanding receipt in the platform's private persistent storage; no UI implementation here.


## 8 Exact schema and integration API

All listed fields are required, case sensitive, and exclusive to that message
type; omitted, unknown, duplicate (after JSON string decoding), case variants,
null, trailing JSON, invalid UTF-8 and unpaired UTF-16 surrogates are errors.
Explicitly nullable notification fields below are the only exceptions. JSON
member ordering and insignificant whitespace are not cryptographic encodings.
`v` is the JSON integer `1`, not a string or floating-point/exponent spelling.
`type` is a literal string; suite is implicit in v and cannot be negotiated.
Unsigned decimal strings fit uint64 and contain only `0` or a nonzero digit
followed by digits; no leading zeros, signs, decimal points or escaped digits.
Base64url decoded JSON string values have no padding/whitespace, use only
the URL alphabet and have zero unused pad bits. JSON string escapes are allowed
when the resulting value is canonical; canonical decimal counter strings
additionally require literal digits as stated above. Byte lengths below refer to decoded values.

| Fields | Representation |
| --- | --- |
| registration, connection, qid, aid, window, pair_epoch | 16 bytes, base64url |
| ru, rh, nu, nh, request, reply, receipt token | 32 bytes, base64url |
| C, proofH, proofU, session | 48 bytes, base64url |
| pkH, pkU | 1184 bytes, canonical FIPS 203 ML-KEM-768 encapsulation key; validate coefficients |
| ctH, ctU | 1088 bytes, raw FIPS 203 ciphertext |
| host_fp, ui_id | 96 lowercase hexadecimal characters |
| seq, id, bytes, through, notification id/ticket | canonical uint64 decimal strings |
| payload | 1..65536 opaque bytes, base64url; endpoint decodes one Inner object |
| notification.reply body | 1..524288 opaque bytes, base64url; UI decodes one NotificationEnvelope |
| ciphertext | GCM sealed bytes including the 16-byte tag; record must fit the inner message limit |

Exact Inner variants (in addition to v and type):

| type | Fields |
| --- | --- |
| pair.commit | aid, C |
| pair.host | aid, window, pair_epoch, pkH, rh |
| pair.reveal | aid, pkU, ru |
| auth.init, pair.auth.init | host_fp, ui_id, pair_epoch, nu, ctH |
| auth.challenge, pair.auth.challenge | nh, ctU, proofH |
| auth.finish, pair.auth.finish | proofU |
| record | session, seq, ciphertext |

Exact encrypted PlainRecord variants also include v and type:

| type | Fields |
| --- | --- |
| ready | pair_epoch, state, aid |
| ready.ack | pair_epoch |
| pair.accepted | aid, host_fp, ui_id, pair_epoch |
| stream.open, stream.opened, stream.fin | id |
| stream.data | id, data |
| stream.credit | id, bytes |
| stream.reset | id, code |

`state` is `active`. The primitive codec retains reserved legacy confirmation
syntax for compatibility with its foundation fixtures; the host does not send
or accept pair.confirm/pair.confirmed and grants them no authority.
All stream ids are positive odd integers. Credit grants are 1..65536; data is
1..16384 bytes. Fixed stream reset codes are `cancelled`, `refused`, `internal`,
`closed`. Fixed relay error codes are `id_in_use`, `host_offline`,
`protocol_error`, `overloaded`. Never forward arbitrary error text.
The four notification control types from section 6 are also Control variants:
`notification.get` has qid, host_id, ui_id, request_header, request_mac;
`notification.reply` has qid, body; `notification.empty` and
`notification.cancel` have only qid. request_header/request_mac are the exact
ASCII HTTP header values, not decoded/reencoded JSON. The request header is
at most 4096 ASCII characters (at most 3072 decoded JSON bytes), and MAC is
exactly 48 decoded bytes. The relay validates base64url/size syntax without
parsing endpoint JSON or authenticating it.

Notification request is exactly `{v,pair_epoch,request,ack}` (no type field).
Ack is null or exactly `{batch,through,token}`; batch is 16 bytes, token 32
bytes, through is positive. NotificationEnvelope is exactly
`{v,host_fp,ui_id,pair_epoch,request,reply,ciphertext}` (no type). Plaintext page
is exactly `{batch,through,token,notifications,more,acknowledged}`, or exactly
`{error:"invalid_ack"}`. Empty page has null batch/token, notifications `[]`,
more false and the current cursor. Acknowledged is null or a 16-byte batch id.
A nonempty page has a 16-byte batch and 32-byte token, 1..50 strictly increasing
notification ids and through equal to the last included id. For a missing
source record the item is exactly `{id,unavailable:true}`; otherwise all
projection fields in section 6 are required, with unavailable false. Roles
are `user`, `ceo`, `pm`, `developer`, `reviewer`, `system`. Booleans are JSON
booleans and created_at is a UTC RFC3339 string ending in `Z`.

The following are exported by `internal/relayproto`; use these codecs rather
than marshaling the union structs directly. Every codec returns an error on
invalid wire values. All cryptographic entry points validate canonical Route,
key, nonce, epoch and mode lengths. `NewRoute(origin, hostID, allowLoopbackHTTP)`
canonicalizes ASCII hostname case and removes the default port; it rejects userinfo,
path (including `/`), query and fragment. Insecure HTTP is accepted only with
explicit opt-in for localhost or a literal loopback IP. Call it at configuration
time; never silently accept an endpoint-provided replacement route. DNS hosts
must use ASCII labels (letters, digits, interior hyphens, 1..63 characters
per label, 253 total), including preconverted IDNA A-labels. Unicode, underscores,
empty labels and leading/trailing hyphens are rejected. Do not independently
IDNA-convert a configured origin when building a transcript.

```go
const Version uint16 = 1
const Suite = "MLKEM768-HKDF384-AES256GCM"
type Bytes []byte          // canonical unpadded base64url JSON string
type Counter uint64        // canonical decimal JSON string
type Route struct { Origin, HostID string }
func NewRoute(string, string, bool) (Route, error)
func EncodeFields(...[]byte) []byte // E, locally bounded fields

func GenerateIdentity() (*Identity, error)
func ImportIdentity(seed []byte) (*Identity, error)
func (*Identity) Seed() []byte
func (*Identity) PublicKey() []byte
func ValidatePublicKey([]byte) error
func Fingerprint(role string, publicKey []byte) (string, error) // host or ui

func EncodeControl(Control) ([]byte, error)
func DecodeControl([]byte) (Control, error)
func EncodeInner(Inner) ([]byte, error)
func DecodeInner([]byte) (Inner, error)
func EncodePlain(PlainRecord) ([]byte, error)
func DecodePlain([]byte) (PlainRecord, error)

type RecordIO interface {
    Send(context.Context, PlainRecord) error
    Receive(context.Context) (PlainRecord, error)
    Close() error
}
```

The Control/Inner/PlainRecord Go structs contain the union of fields above;
field names use Go initialisms: HostID, UIID, HostFP, AID, QID, PKH, PKU, CTH,
CTU, NU, NH, RU, RH, ProofH, ProofU, PairEpoch, Session, Seq, ID, Bytes, Data,
Payload, Body, Registration, Connection, RequestHeader, RequestMAC, State,
Code, V and Type. Irrelevant nonzero struct fields are rejected when encoding.
`RecordIO` is an interface only; the host supplies the authenticated network
adapter. It serializes the entire Seal-and-write operation, including control
and all streams, under one send lock. Do not seal concurrently and then write
in completion order: ciphertext order must match seq order. A failed or
uncertain send closes the session; never discard a sealed record and continue.
Exactly one consumer calls Receive and demultiplexes stream/control records.

Exported sentinel errors are `ErrProtocol` and `ErrAuthentication`; callers
may use `errors.Is`. Exported limits are `MaxControlSize` (1048576),
`MaxInnerSize` (65536), `MaxNotificationSize` (524288),
`MaxNotificationPlaintext` (262144), `MaxStreamData` (16384), `MaxStreams` (64),
`InitialCredit` (65536), `MaxRecords` (16777216) and `MaxRecordBytes`
(68719476736). PairPhase constants are `PairClosed`, `PairArmed`,
`PairCommitted`, `PairRevealed`, `PairProved`, `PairAccepted`, `PairSpent`, with
the lowercase string values shown in section 4.

Pairing interfaces:

```go
func PairCommitment(route Route, aid, pkU, ru []byte) ([]byte, error)
type PairTranscript struct {
    Route Route
    AID, Window, PairEpoch, C, PKU, RU, PKH, RH Bytes
}
func (PairTranscript) Encode() ([]byte, error) // exact P
func (PairTranscript) Hash() ([]byte, error)  // SHA-384(P)
func (PairTranscript) PIN() (string, error)
func (PairTranscript) Clone() PairTranscript
func (PairTranscript) AuthContext() (AuthContext, error)

func NewPairUI(Route, *Identity, io.Reader) (*PairUI, Inner, error)
func (*PairUI) Reveal(Inner) (Inner, error)
func (*PairUI) Transcript() (PairTranscript, error)
type PairUIState struct { Transcript PairTranscript; Used bool }
func (*PairUI) Snapshot() PairUIState
func RestorePairUI(PairUIState) (*PairUI, error)

type PairSlot struct {
    Phase PairPhase
    Expires time.Time
    Connection string
    Transcript PairTranscript
    NotificationRoot Bytes
}
func (PairSlot) Clone() PairSlot
// Update isolates the callback copy and returns only after atomic durable save.
// Callback or save failure commits nothing. It serializes the entire host.
type PairStore interface { Update(func(*PairSlot) error) error }
func NewPairHost(Route, *Identity, PairStore, func() time.Time, io.Reader) (*PairHost, error)
func (*PairHost) Begin() error
func (*PairHost) Commit(connection string, message Inner) (Inner, error)
func (*PairHost) Reveal(connection string, message Inner) (PairTranscript, error)
func (*PairHost) Prove(connection string, proof *Authenticated) error
func (*PairHost) Accept(connection string, aid []byte, pin string, fullPK []byte) (PairAction, error)
func (*PairHost) Abort() error
func (*PairHost) Recover() error

type PairAcceptance struct { Transcript PairTranscript; NotificationRoot Bytes }
type PairAction struct { Acceptance *PairAcceptance }
```

`NewPairUI` returns the commit action. Privately persist Snapshot before sending
it, and persist the frozen Used snapshot before sending Reveal's return value.
Used persists across reconstruction and prevents another reveal, even if a
response was invalid. Never automatically clone/restart an old attempt from
an earlier unused snapshot. A caller starting a new attempt must have new
human authorization and new randomness. Pure transcript/PIN helpers do not
grant trust; only show the PIN after mutual proof.

One PairHost owns the whole host, not a UI connection. Its injected store is
host-global, serial, durable and transactional; store adapters should use
PairSlot.Clone for isolated mutable copies. Call Recover on startup before
handling remote traffic; it burns unfinished slots. Begin is local only and
sets an absolute five-minute expiry; methods check it at their transition.
Consumers schedule expiry/cancel/disconnect by calling Abort, including every
failed/abandoned pairing handshake. No successful remote transition arms a
slot. Commit returns its reply only after consumed state is saved. Reveal
freezes verified contributions. Prove requires the host-side verified proof
for exactly this transcript. Accept requires exactly one of six-digit PIN or
full raw public key and returns acceptance material after saving accepted.
Wrong/out-of-order messages and invalid local acceptance save spent rather
than rolling back consumption. Every storage error latches that PairHost
closed: repair storage, reconstruct it, and Recover before resuming.

Acceptance material is not a capability or local CLI success. The consumer
must durably save its device record/root as active after local acceptance
before acknowledging success or granting access. A crash between slot
acceptance and device-record save fails closed and may require a fresh local
ceremony; it must never restore a consumed slot. Accept returns only Acceptance.
No policy in this package activates a device.

Authentication and record interfaces:

```go
type AuthContext struct {
    Route Route
    Mode string // pair or reconnect
    PKH, PKU, PairEpoch, PairHash Bytes
}
func (AuthContext) Clone() AuthContext
func (AuthContext) Encode() ([]byte, error) // B
func NewUIHandshake(AuthContext, *Identity, io.Reader) (*UIHandshake, Inner, error)
func (*UIHandshake) Finish(challenge Inner) (Inner, *Authenticated, error)
func (*UIHandshake) Close()
func NewHostHandshake(AuthContext, *Identity, io.Reader) (*HostHandshake, error)
func (*HostHandshake) Challenge(init Inner) (Inner, error)
func (*HostHandshake) Finish(finish Inner) (*Authenticated, error)
func (*HostHandshake) Close()
func (*Authenticated) SessionID() []byte
func (*Authenticated) NotificationRoot() ([]byte, error) // only mode=pair
func (*Authenticated) NewSession() (*Session, error)     // exactly once
func (*Authenticated) Close()
func (*Session) Seal(PlainRecord) (Inner, error)
func (*Session) Open(Inner) (PlainRecord, error)
func (*Session) Close() error
```

On reconnect the host looks up ui_id and epoch, loads saved pkU and supplies
its own saved pkH. No untrusted message can replace either. Host result exists
only after successful UI proof verification; UI result exists only after
successful host proof verification. Role is bound to the result internally.
PairHash must be exactly 48 bytes in pair mode and empty on reconnect.
PairHost.Prove accepts only a host-side result bound to the frozen pair context.
The pairing root is never regenerated during reconnect. Close handshake helpers
on cancellation to release secrets; after deriving a session and (for pairing)
notification root, Close the Authenticated object. Clear temporary key material
and release cipher references on Close; Go does not promise physical erasure
of all heap/cipher copies.

A result can create one Session, preventing counter reset under reused keys.
Session has one mutex across both directions and global per-direction seq and
plaintext-byte accounting. Incoming values, tag and plaintext schema must all
validate before receive counters advance. Any Seal/Open failure terminates
the session. Maximum usage is 2^24 records and 2^36 plaintext bytes per direction;
establish a fresh handshake before the next record would exceed either bound.
Ready-state, stream state/flow control and device authority are enforced by
the consumer demultiplexer, not the cryptographic Session.

A nil randomness reader selects crypto/rand.Reader for pairing, nonce and
notification reply randomness. Tests may inject a reader; production must use
a cryptographic source and never an endpoint-selected deterministic override.
ML-KEM encapsulation always uses the standard library's cryptographic RNG.

Notification interfaces:

```go
type Poll struct { RequestHeader, RequestMAC string }
type NotificationContext struct {
    Route Route
    HostFP, UIID string
    PairEpoch Bytes
}
func (NotificationContext) Clone() NotificationContext
type Receipt struct { Batch Bytes; Through Counter; Token Bytes }
func (Receipt) Equal(Receipt) bool // compares complete tuple, constant-time tokens
type NotificationRequest struct {
    V uint16
    PairEpoch, Request Bytes
    Ack *Receipt
}
func NewPoll(context NotificationContext, root, freshRequest []byte, ack *Receipt) (Poll, error)
func VerifyPoll(context NotificationContext, root []byte, poll Poll) (NotificationRequest, error)
func EncodeNotificationPage(NotificationPage) ([]byte, error)
func DecodeNotificationPage([]byte) (NotificationPage, error)
func SealNotification(context NotificationContext, root, request []byte, page NotificationPage, random io.Reader) (NotificationEnvelope, error)
func OpenNotification(context NotificationContext, root, outstandingRequest []byte, envelope NotificationEnvelope) (NotificationPage, error)
func EncodeNotificationEnvelope(NotificationEnvelope) ([]byte, error)
func DecodeNotificationEnvelope([]byte) (NotificationEnvelope, error)
```

NotificationPage has Batch/Token/Acknowledged Bytes, Through Counter,
Notifications []Notification, More bool and Error string. Notification has ID
and Ticket Counter, AuthorRole/CreatedAt/Title/Body string, and
BodyTruncated/TitleTruncated/Unavailable bool. NotificationEnvelope has V uint16,
HostFP/UIID string, and PairEpoch/Request/Reply/Ciphertext Bytes. These types
follow the exact schemas above. Use `Receipt.Equal` only for validated stored
receipts; absent last_ack/pending is represented separately, never an all-zero
tuple. VerifyPoll proves root possession and binds the request; it does not
check current authorization or mutate cursor/last_ack/pending. The consumer
must load active device/epoch before calling it, serialize the section 6 state
machine and recheck revocation before saving/sending. SealNotification uses a
new 32-byte reply random and independent key even on repeated request ids.
OpenNotification requires the current outstanding request, not an arbitrary id
from the received envelope. Status codes, network success and receipt tokens
alone never confer authority.

## 9 Interoperability fixtures and evidence limits

Fixtures live in `internal/relayproto/testdata`:

* `mlkem768-acvp.json`: selected ML-KEM-768 key generation, deterministic
  encapsulation, valid/modified ciphertext decapsulation and public-key
  validation vectors from the NIST ACVP-Server repository, pinned by commit
  and source URLs inside the file. Seeds are d||z (64 bytes). The included
  dk fields are **expanded 2400-byte FIPS keys**, for native implementations
  whose decapsulation interface consumes that form; they are not valid
  ImportIdentity seeds. Go tests exercise keygen/encapsulation/key-validation
  against those authoritative answers; expanded-key decapsulation cases are
  native-consumer fixtures, since Go's public import accepts seeds only.
* `protocol-v1.json`: all binary fields are lowercase hex except explicitly
  named wire/header/MAC strings. It includes NIST-derived host/UI seeds/keys,
  deterministic KEM inputs/outputs, exact P/B/T bytes, commitments, pair hash,
  **PIN `007026`**, PRK, both finished proofs, both directional keys, record
  AAD/plaintext/ciphertext/wire, notification root/request key, request
  headers/MAC and response key/AAD/plaintext/ciphertext. E/SHA-384/HMAC/HKDF/PIN
  were derived independently with Python's standard library; GCM answers
  were produced independently through OpenSSL EVP AES-256-GCM. KEM known
  answers use Go's standard mlkemtest only in tests, checked against ACVP.
* `negative-v1.json`: explicit wire strings and rejection reasons for strict
  duplicate/unknown/UTF/number/base64/schema checks. Focused behavioral tests
  additionally cover contribution reuse, wrong proofs/context, replay/gaps,
  reflection, quota boundaries and notification request/envelope tampering.

The primitive standard is [FIPS 203](https://csrc.nist.gov/pubs/fips/203/final).
Authoritative algorithm vectors are [NIST ACVP-Server ML-KEM test data](https://github.com/usnistgov/ACVP-Server/tree/master/gen-val/json-files).
Known-answer encapsulation uses the [Go 1.26.2 mlkemtest API](https://pkg.go.dev/crypto/mlkem/mlkemtest@go1.26.2).
These fixtures establish primitive and encoding interoperability; they do not
constitute an independent audit or NIST certification of this custom protocol.
