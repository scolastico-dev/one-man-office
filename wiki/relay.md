# Relay deployment

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
