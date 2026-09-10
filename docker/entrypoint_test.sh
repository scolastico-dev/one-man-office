#!/usr/bin/env bash

set -Eeuo pipefail

image="${1:-omo:container-test}"
container=""
fixture="$(mktemp -d)"
host_uid="$(id -u)"
host_gid="$(id -g)"

cleanup() {
    if [[ -n "$container" ]]; then
        docker rm -f "$container" >/dev/null 2>&1 || true
    fi
    rm -rf "$fixture"
}
trap cleanup EXIT

fail() {
    printf 'entrypoint test failed: %s\n' "$*" >&2
    exit 1
}

docker image inspect "$image" >/dev/null

unknown_output="$(docker run --rm \
    -e OMO_AGENT_CLIS=' ,future-agent, ' \
    "$image" --help 2>&1)" || fail "unknown agent must not prevent startup"
[[ "$unknown_output" == *'unknown agent CLI "future-agent"; skipping'* ]] ||
    fail "unknown agent warning was not emitted"

mkdir -p "$fixture/result"
printf '#!/usr/bin/env bash\nprintf "path\\n" >> /result/order\n' >"$fixture/init.sh"
chmod +x "$fixture/init.sh"
docker run --rm \
    -v "$fixture/init.sh:/fixture/init.sh:ro" \
    -v "$fixture/result:/result" \
    -e INIT_SCRIPT_PATH=/fixture/init.sh \
    -e $'INIT_SCRIPT=printf "inline\\n" >> /result/order' \
    "$image" --help >/dev/null
[[ "$(cat "$fixture/result/order")" == $'path\ninline' ]] ||
    fail "path and inline init scripts did not run in order"

set +e
docker run --rm -e INIT_SCRIPT='exit 23' "$image" --help >/dev/null 2>&1
status=$?
set -e
[[ "$status" -eq 23 ]] || fail "init failure returned $status instead of 23"

mkdir -p "$fixture/home" "$fixture/target"
ln -s /target/root-write "$fixture/home/.bashrc"
set +e
docker run --rm \
    -v "$fixture/home:/home/omo" \
    -v "$fixture/target:/target" \
    -e OMO_UID="$host_uid" \
    -e OMO_GID="$host_gid" \
    "$image" --help >/dev/null 2>&1
status=$?
set -e
[[ "$status" -ne 0 ]] || fail "symlinked user profile was accepted"
[[ ! -e "$fixture/target/root-write" ]] || fail "root followed a user-home symlink"

mkdir -p "$fixture/home-nvm" "$fixture/target/nvm"
ln -s /target/nvm "$fixture/home-nvm/.nvm"
set +e
docker run --rm \
    -v "$fixture/home-nvm:/home/omo" \
    -v "$fixture/target:/target" \
    -e OMO_UID="$host_uid" \
    -e OMO_GID="$host_gid" \
    "$image" --help >/dev/null 2>&1
status=$?
set -e
[[ "$status" -ne 0 ]] || fail "symlinked NVM directory was accepted"

container="$(docker run -d --rm -e OMO_UID=12345 -e OMO_GID=12345 "$image" --listen 0.0.0.0:0)"
for _ in {1..50}; do
    uid="$(docker exec "$container" awk '/^Uid:/ {print $2}' /proc/1/status 2>/dev/null || true)"
    if [[ "$uid" == "12345" ]]; then
        docker exec "$container" su-exec omo bash -lc \
            'command -v node >/dev/null && command -v pnpm >/dev/null && command -v nvm >/dev/null' ||
            fail "Node, pnpm, or NVM is unavailable to the runtime user"
        printf 'entrypoint tests passed\n'
        exit 0
    fi
    sleep 0.1
done

docker logs "$container" >&2 || true
fail "company did not remain running"
