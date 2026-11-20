#!/usr/bin/env bash
# Exercise the actual executable and separate image with admission off and on.
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/omo-relay-test.XXXXXX")"
image="omo-relay-test:$$"
app_pid=""
logs_pid=""
container_id=""
image_created=""
cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [[ -n "$logs_pid" ]]; then kill "$logs_pid" 2>/dev/null || true; wait "$logs_pid" 2>/dev/null || true; fi
    if [[ -n "$app_pid" ]]; then kill -TERM "$app_pid" 2>/dev/null || true; wait "$app_pid" 2>/dev/null || true; fi
    if [[ -n "$container_id" ]]; then docker rm -fv "$container_id" >/dev/null 2>&1 || true; fi
    if [[ -n "$image_created" ]]; then docker image rm "$image" >/dev/null 2>&1 || true; fi
    rm -rf "$work_dir"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

make relay
go test -c -o "$work_dir/probe" ./internal/relayserver
printf 'relay-test-admission-secret\n' > "$work_dir/password"
# The private parent is 0700. The non-root image can read this individual,
# read-only test secret; no host directory is mounted as container data.
chmod 0644 "$work_dir/password"

probe() {
    RELAY_TEST_URL="$1" RELAY_TEST_PASSWORD_FILE="$2" \
        "$work_dir/probe" -test.run '^TestDeploymentProbe$' -test.v -test.timeout 60s
}

for mode in open protected; do
    password_file=""
    if [[ "$mode" == protected ]]; then password_file="$work_dir/password"; fi
    coproc RELAY_APP { exec "$repo_root/bin/omo-relay" -listen 127.0.0.1:0 -password-file "$password_file" 2>&1; }
    app_pid="$RELAY_APP_PID"
    IFS= read -r -t 30 ready <&"${RELAY_APP[0]}"
    [[ "$ready" == 'omo-relay listening on '* ]]
    probe "http://${ready#omo-relay listening on }" "$password_file"
    kill -TERM "$app_pid"
    wait "$app_pid"
    app_pid=""
done

image_created=yes
make relay-image RELAY_IMAGE="$image"

for mode in open protected; do
    password_file=""
    mount_args=()
    command_args=()
    if [[ "$mode" == protected ]]; then
        password_file="$work_dir/password"
        mount_args=(--mount "type=bind,src=$password_file,dst=/run/secrets/relay_password,readonly")
        command_args=(-password-file /run/secrets/relay_password)
    fi
    container_id="$(docker run --rm --detach --read-only \
        --tmpfs /tmp:rw,noexec,nosuid,size=1m \
        --cap-drop ALL --security-opt no-new-privileges:true \
        --publish 127.0.0.1::8091 "${mount_args[@]}" "$image" "${command_args[@]}")"
    coproc RELAY_LOGS { exec docker logs --follow "$container_id" 2>&1; }
    logs_pid="$RELAY_LOGS_PID"
    IFS= read -r -t 30 ready <&"${RELAY_LOGS[0]}"
    [[ "$ready" == 'omo-relay listening on '* ]]
    kill "$logs_pid" 2>/dev/null || true
    wait "$logs_pid" 2>/dev/null || true
    logs_pid=""
    address="$(docker port "$container_id" 8091/tcp)"
    probe "http://$address" "$password_file"
    # The scratch image must include the notices for the code it links.
    docker cp "$container_id:/usr/share/doc/omo-relay/THIRD_PARTY_NOTICES" "$work_dir/notices"
    [[ -s "$work_dir/notices" ]]
    docker stop --time 10 "$container_id" >/dev/null
    container_id=""
done

docker compose -f docker/relay-compose.yaml config --quiet
printf 'relay executable and container tests passed (open and protected)\n'
