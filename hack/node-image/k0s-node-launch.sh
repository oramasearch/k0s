#!/bin/sh

# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0

set -eu

# shellcheck source=/dev/null
. /etc/k0s/node-config.env

case "$K0S_NODE_ROLE" in
    controller)
        exec /usr/local/bin/k0s controller
        ;;
    controller+worker)
        if [ -n "$K0S_JOIN_TOKEN_FILE" ]; then
            exec /usr/local/bin/k0s controller --enable-worker --token-file "$K0S_JOIN_TOKEN_FILE"
        fi
        exec /usr/local/bin/k0s controller --enable-worker
        ;;
    worker)
        exec /usr/local/bin/k0s worker --token-file "$K0S_JOIN_TOKEN_FILE"
        ;;
    *)
        echo "unsupported K0S_NODE_ROLE: $K0S_NODE_ROLE" >&2
        exit 1
        ;;
esac
