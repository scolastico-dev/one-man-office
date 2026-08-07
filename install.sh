#!/bin/sh

set -eu

REPOSITORY="scolastico-dev/one-man-office"
BINARY="omo"
INSTALL_DIR="${OMO_INSTALL_DIR:-$HOME/.local/bin}"
API_URL="https://api.github.com/repos/$REPOSITORY/releases/latest"

download_stdout() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$1"
    else
        echo "error: curl or wget is required" >&2
        exit 1
    fi
}

download_file() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$2" "$1"
    else
        echo "error: curl or wget is required" >&2
        exit 1
    fi
}

case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *)
        echo "error: unsupported operating system: $(uname -s)" >&2
        exit 1
        ;;
esac

case "$(uname -m)" in
    x86_64 | amd64) arch="amd64" ;;
    arm64 | aarch64) arch="arm64" ;;
    *)
        echo "error: unsupported architecture: $(uname -m)" >&2
        exit 1
        ;;
esac

release_json="$(download_stdout "$API_URL")"
tag="$(printf '%s\n' "$release_json" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
if [ -z "$tag" ]; then
    echo "error: could not determine the latest release" >&2
    exit 1
fi

version="${tag#v}"
asset="$BINARY-$os-$arch.tar.gz"
release_url="https://github.com/$REPOSITORY/releases/download/$tag"
tmp_dir="$(mktemp -d 2>/dev/null || mktemp -d -t omo-install)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

archive="$tmp_dir/$asset"
checksums="$tmp_dir/SHA256SUMS"
download_file "$release_url/$asset" "$archive"
download_file "$release_url/SHA256SUMS" "$checksums"

expected="$(awk -v name="./$asset" '$2 == name { print $1 }' "$checksums")"
if [ -z "$expected" ]; then
    echo "error: release checksum for $asset was not found" >&2
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive" | awk '{ print $1 }')"
else
    echo "error: sha256sum or shasum is required" >&2
    exit 1
fi

if [ "$actual" != "$expected" ]; then
    echo "error: checksum verification failed for $asset" >&2
    exit 1
fi

tar -xzf "$archive" -C "$tmp_dir"

mkdir -p "$INSTALL_DIR"
target="$INSTALL_DIR/$BINARY"
installed_version=""
if [ -x "$target" ]; then
    installed_version="$("$target" --version 2>/dev/null | awk '{ print $NF }' | sed 's/^v//' || true)"
fi

if [ "$installed_version" = "$version" ]; then
    echo "$BINARY $tag is already installed at $target"
else
    install -m 0755 "$tmp_dir/$BINARY" "$target"
    if [ -n "$installed_version" ]; then
        echo "updated $BINARY from $installed_version to $version at $target"
    else
        echo "installed $BINARY $version at $target"
    fi
fi

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        case "${SHELL:-}" in
            */zsh) profile="$HOME/.zshrc" ;;
            */bash) profile="$HOME/.bashrc" ;;
            *) profile="$HOME/.profile" ;;
        esac
        path_line="export PATH=\"$INSTALL_DIR:\$PATH\""
        if ! grep -Fqx "$path_line" "$profile" 2>/dev/null; then
            printf '\n# omo\n%s\n' "$path_line" >> "$profile"
            echo "added $INSTALL_DIR to PATH in $profile (restart your shell to apply)"
        fi
        ;;
esac
