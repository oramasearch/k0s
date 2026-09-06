#!/bin/sh

# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0

set -eu

if [ "$#" -ne 3 ]; then
    echo "usage: $0 K0S_BINARY IMAGE_REFERENCE NODE_CONFIG_SIGNING_PUBLIC_KEY" >&2
    exit 2
fi

k0s_binary=$1
image_reference=$2
signing_key=$3
builder=${NODE_IMAGE_BUILDER:-podman}
script_dir=$(dirname -- "$0")
script_dir=$(CDPATH='' cd -- "$script_dir" && pwd)
context_dir=$(mktemp -d "${TMPDIR:-/tmp}/k0s-node-image.XXXXXX")
trap 'rm -rf -- "$context_dir"' EXIT HUP INT TERM

if [ ! -x "$k0s_binary" ]; then
    echo "k0s binary is not executable: $k0s_binary" >&2
    exit 1
fi

if [ ! -r "$signing_key" ]; then
    echo "node-config signing public key is not readable: $signing_key" >&2
    exit 1
fi

cp "$k0s_binary" "$context_dir/k0s"
cp "$signing_key" "$context_dir/node-config-signing.pub"
cp "$script_dir/Containerfile" "$context_dir/Containerfile"
cp "$script_dir/k0s-node-launch.sh" "$context_dir/k0s-node-launch.sh"
cp "$script_dir/sshd_config" "$context_dir/sshd_config"
cp -R "$script_dir/systemd" "$context_dir/systemd"

"$builder" build --file "$context_dir/Containerfile" --tag "$image_reference" "$context_dir"
digest=$("$builder" image inspect --format '{{.Digest}}' "$image_reference")

case "$digest" in
    sha256:????????????????????????????????????????????????????????????????)
        ;;
    *)
        echo "builder did not return an sha256 image digest: $digest" >&2
        exit 1
        ;;
esac

printf '%s@%s\n' "${image_reference%@*}" "$digest"
