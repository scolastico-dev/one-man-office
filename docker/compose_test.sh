#!/usr/bin/env bash

set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/compose.yml"

model="$(docker compose -f "$compose_file" config --format json)"
jq -e '.services.docker.volumes[] | select(.type == "bind" and .target == "/workspace")' \
    <<<"$model" >/dev/null || {
    printf 'compose test failed: DinD does not share /workspace with omo\n' >&2
    exit 1
}

jq -e '.services.omo.volumes[] | select(.type == "bind" and .target == "/home/omo")' \
    <<<"$model" >/dev/null || {
    printf 'compose test failed: omo user home is not a bind mount\n' >&2
    exit 1
}

git -C "$repo_root" check-ignore --quiet docker-data/home || {
    printf 'compose test failed: default persisted home is not ignored by Git\n' >&2
    exit 1
}

printf 'compose tests passed\n'
