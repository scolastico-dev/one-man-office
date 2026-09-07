#!/usr/bin/env bash

set -Eeuo pipefail

readonly runtime_user="omo"
readonly runtime_group="omo"
readonly runtime_home="/home/omo"
readonly runtime_workspace="/workspace"
readonly nvm_source="/usr/local/share/nvm"
readonly claude_install_url="https://claude.ai/install.sh"
readonly codex_install_url="https://chatgpt.com/codex/install.sh"
readonly gemini_registry_url="https://registry.npmjs.org/@google%2fgemini-cli"

runtime_uid="${OMO_UID:-1000}"
runtime_gid="${OMO_GID:-1000}"

die() {
    printf 'omo container: %s\n' "$*" >&2
    exit 1
}

validate_id() {
    local label="$1"
    local value="$2"
    [[ "$value" =~ ^[1-9][0-9]*$ ]] || die "$label must be a positive integer"
}

create_runtime_user() {
    validate_id OMO_UID "$runtime_uid"
    validate_id OMO_GID "$runtime_gid"

    if awk -F: -v id="$runtime_gid" '$3 == id { found = 1 } END { exit !found }' /etc/group; then
        die "OMO_GID $runtime_gid is already assigned"
    fi
    if awk -F: -v id="$runtime_uid" '$3 == id { found = 1 } END { exit !found }' /etc/passwd; then
        die "OMO_UID $runtime_uid is already assigned"
    fi

    addgroup -g "$runtime_gid" "$runtime_group"
    adduser -D -H -u "$runtime_uid" -G "$runtime_group" -h "$runtime_home" -s /bin/bash "$runtime_user"
    [[ ! -L "$runtime_home" ]] || die "$runtime_home must not be a symbolic link"
    install -d -m 0755 -o "$runtime_uid" -g "$runtime_gid" "$runtime_home" "$runtime_workspace"
    chown -hR "$runtime_uid:$runtime_gid" "$runtime_home"
}

require_safe_directory() {
    local path="$1"
    [[ ! -L "$path" ]] || die "$path must not be a symbolic link"
    [[ ! -e "$path" || -d "$path" ]] || die "$path must be a directory"
}

require_safe_file() {
    local path="$1"
    [[ ! -L "$path" ]] || die "$path must not be a symbolic link"
    [[ ! -e "$path" || -f "$path" ]] || die "$path must be a regular file"
}

configure_runtime_home() {
    require_safe_directory "$runtime_home/.local"
    require_safe_directory "$runtime_home/.local/bin"
    require_safe_directory "$runtime_home/.local/share"
    require_safe_directory "$runtime_home/.local/share/pnpm"
    require_safe_directory "$runtime_home/.nvm"
    require_safe_file "$runtime_home/.nvm/nvm.sh"
    require_safe_file "$runtime_home/.bashrc"
    require_safe_file "$runtime_home/.bash_profile"

    run_as_runtime_user install -d -m 0755 \
        "$runtime_home/.local/bin" "$runtime_home/.local/share/pnpm" "$runtime_home/.nvm"
    if [[ ! -s "$runtime_home/.nvm/nvm.sh" ]]; then
        if find "$runtime_home/.nvm" -mindepth 1 -print -quit | grep -q .; then
            die "$runtime_home/.nvm is incomplete; remove it before restarting"
        fi
        run_as_runtime_user cp -a "$nvm_source/." "$runtime_home/.nvm/"
    fi

    local marker="# omo container environment"
    local profile="$runtime_home/.bashrc"
    if ! grep -Fqx "$marker" "$profile" 2>/dev/null; then
        # Positional parameters are intentionally expanded by the child shell.
        # shellcheck disable=SC2016
        run_as_runtime_user bash -c 'cat >>"$1"' bash "$profile" <<'EOF'
# omo container environment
export NVM_DIR="$HOME/.nvm"
export NPM_CONFIG_PREFIX="$HOME/.local"
export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$HOME/.local/bin:$PNPM_HOME:$PATH"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
EOF
    fi
    # Keep HOME literal so the persisted profile resolves it for its future user.
    # shellcheck disable=SC2016
    local login_source='[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"'
    if ! grep -Fqx "$login_source" "$runtime_home/.bash_profile" 2>/dev/null; then
        # Positional parameters are intentionally expanded by the child shell.
        # shellcheck disable=SC2016
        run_as_runtime_user bash -c 'printf "%s\n" "$1" >>"$2"' bash \
            "$login_source" "$runtime_home/.bash_profile"
    fi
}

run_as_runtime_user() {
    su-exec "$runtime_user" env \
        HOME="$runtime_home" \
        USER="$runtime_user" \
        LOGNAME="$runtime_user" \
        NVM_DIR="$runtime_home/.nvm" \
        NPM_CONFIG_PREFIX="$runtime_home/.local" \
        PNPM_HOME="$runtime_home/.local/share/pnpm" \
        PATH="$runtime_home/.local/bin:$runtime_home/.local/share/pnpm:$PATH" \
        "$@"
}

runtime_has() {
    # CLI_NAME is intentionally expanded by the child shell after su-exec.
    # shellcheck disable=SC2016
    run_as_runtime_user env CLI_NAME="$1" sh -c 'command -v "$CLI_NAME" >/dev/null 2>&1'
}

install_remote_script() {
    local name="$1"
    local url="$2"
    local installer
    installer="$(mktemp)"
    if ! curl --proto '=https' --tlsv1.2 -fsSL "$url" -o "$installer"; then
        rm -f "$installer"
        die "failed to download the $name installer"
    fi
    chown "$runtime_uid:$runtime_gid" "$installer"
    if ! run_as_runtime_user bash "$installer"; then
        rm -f "$installer"
        die "$name installation failed"
    fi
    rm -f "$installer"
}

install_gemini() {
    local metadata archive tarball
    metadata="$(mktemp)"
    archive="$(mktemp --suffix=.tgz)"
    if ! curl --proto '=https' --tlsv1.2 -fsSL "$gemini_registry_url" -o "$metadata"; then
        rm -f "$metadata" "$archive"
        die "failed to resolve the Gemini CLI release"
    fi
    tarball="$(jq -er '.["dist-tags"].latest as $version | .versions[$version].dist.tarball' "$metadata")" || {
        rm -f "$metadata" "$archive"
        die "Gemini CLI registry metadata is invalid"
    }
    case "$tarball" in
        https://registry.npmjs.org/*) ;;
        *)
            rm -f "$metadata" "$archive"
            die "Gemini CLI registry returned an unexpected download URL"
            ;;
    esac
    if ! curl --proto '=https' --tlsv1.2 -fsSL "$tarball" -o "$archive"; then
        rm -f "$metadata" "$archive"
        die "failed to download the Gemini CLI package"
    fi
    chown "$runtime_uid:$runtime_gid" "$archive"
    if ! run_as_runtime_user npm install --global "$archive"; then
        rm -f "$metadata" "$archive"
        die "Gemini CLI installation failed"
    fi
    rm -f "$metadata" "$archive"
}

install_agent_clis() {
    local requested agent
    requested="${OMO_AGENT_CLIS:-}"
    IFS=',' read -r -a agents <<<"$requested"
    for agent in "${agents[@]}"; do
        agent="${agent//[[:space:]]/}"
        [[ -n "$agent" ]] || continue
        case "$agent" in
            claude)
                runtime_has claude || install_remote_script Claude "$claude_install_url"
                ;;
            codex)
                runtime_has codex || install_remote_script Codex "$codex_install_url"
                ;;
            gemini)
                runtime_has gemini || install_gemini
                ;;
            *)
                printf 'omo container: unknown agent CLI "%s"; skipping\n' "$agent" >&2
                ;;
        esac
    done
}

run_init_scripts() {
    if [[ -n "${INIT_SCRIPT_PATH:-}" ]]; then
        [[ -f "$INIT_SCRIPT_PATH" && -r "$INIT_SCRIPT_PATH" ]] ||
            die "INIT_SCRIPT_PATH must be a readable regular file: $INIT_SCRIPT_PATH"
        bash -Eeuo pipefail "$INIT_SCRIPT_PATH"
    fi
    if [[ -n "${INIT_SCRIPT:-}" ]]; then
        bash -Eeuo pipefail -c "$INIT_SCRIPT"
    fi
}

main() {
    [[ "$(id -u)" -eq 0 ]] || die "entrypoint must start as root"
    create_runtime_user
    configure_runtime_home
    install_agent_clis
    run_init_scripts

    cd "$runtime_workspace"
    exec su-exec "$runtime_user" env \
        HOME="$runtime_home" \
        USER="$runtime_user" \
        LOGNAME="$runtime_user" \
        NVM_DIR="$runtime_home/.nvm" \
        NPM_CONFIG_PREFIX="$runtime_home/.local" \
        PNPM_HOME="$runtime_home/.local/share/pnpm" \
        PATH="$runtime_home/.local/bin:$runtime_home/.local/share/pnpm:$PATH" \
        omo supervisor --listen 0.0.0.0:8090 "$@"
}

main "$@"
