# Docker

Multi-architecture Alpine images are published to GitHub Container Registry:

```bash
docker pull ghcr.io/scolastico-dev/one-man-office:latest
docker pull ghcr.io/scolastico-dev/one-man-office:nightly
docker pull ghcr.io/scolastico-dev/one-man-office:1.2.3
```

## Compose quick start

Download the Compose example and start the [company dashboard](company.md)
with Docker-in-Docker:

```bash
curl -fsSLo compose.yml https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/compose.yml
docker compose up -d
docker compose logs -f omo
```

When the final command prints the access URL, stop following logs with
`Ctrl-C` and open it after replacing its `0.0.0.0` host with `127.0.0.1`;
preserve the `#...` access key. Set `OMO_WORKSPACE`, `OMO_USER_DIR`,
`OMO_AGENT_CLIS`, and the other variables described below before running
`docker compose up` when you need to override the defaults.

[`compose.yml`](../compose.yml) is a complete Docker-in-Docker example with TLS
wiring. It bind-mounts a workspace and a persistent `/home/omo` user directory
so credentials, NVM-installed Node versions, npm/pnpm packages, and the global
omo home survive recreation. Override the host paths with `OMO_WORKSPACE` and
`OMO_USER_DIR`.

## Image behavior

The image includes Bash, Git, curl/wget, common build tools, Go, Node/npm, NVM,
pnpm, Python, and the Docker CLI. It starts as root only for initialization,
creates an `omo` account using `OMO_UID` and `OMO_GID` (both default to
`1000`), and then starts `omo company --listen 0.0.0.0:8090` as that
account. Additional container arguments are passed to `omo company`.

Agent PTYs and omo's internal worktree/merge Git client receive the configured
Git identity from `agents.env` in `.omo/omo.yaml`, so commits made by agents and
merge commits made by omo use the OMO identity without changing the container
user's global Git configuration. The defaults are `OMO - AI Orchestrator
<omo@scolasti.co>` and disable commit signing through `GIT_CONFIG_PARAMETERS`.

The GitHub `@one-man-office` user is userless and reserved by the omo project.
It is safe to mention it in automation pipelines or CI workflows, for example
in an issue or pull request comment that should trigger an omo-driven CI job.

## Agent CLI provisioning

Set `OMO_AGENT_CLIS` to a comma-separated selection of `claude`, `codex`, and
`gemini`. Selected CLIs that are not already in the persistent user home are
downloaded at startup from their official upstream source. Unknown names are
warned about and skipped.

```bash
docker run --rm -p 127.0.0.1:8090:8090 \
  -e OMO_AGENT_CLIS=claude,codex \
  -e OMO_UID="$(id -u)" -e OMO_GID="$(id -g)" \
  -v "$PWD:/workspace" \
  -v "$PWD/docker-data/home:/home/omo" \
  ghcr.io/scolastico-dev/one-man-office:latest
```

## Init scripts

`INIT_SCRIPT_PATH` may name a mounted Bash script and `INIT_SCRIPT` may contain
inline Bash. They execute as root, after agent CLI installation and before the
company starts; when both are set, the path script runs first. A failure
stops the container. These settings intentionally permit arbitrary root code,
for example `INIT_SCRIPT='apk add --no-cache package-name'`, so treat their
contents as privileged configuration.

## Network exposure

The company dashboard grants terminal and command execution. The example binds
it to host loopback and must not be exposed directly to a network. If remote
access is required, put it behind TLS and effective forward authentication, and
configure the proxy so omo's Host and Origin validation remains intact. See the
[security model](company.md#security-model).
