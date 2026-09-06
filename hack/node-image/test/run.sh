#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

require_variable() {
    local name=$1
    if [[ -z ${!name:-} ]]; then
        echo "required environment variable is unset: $name" >&2
        return 1
    fi
}

require_file() {
    local name=$1
    require_variable "$name"
    if [[ ! -f ${!name} ]]; then
        echo "$name does not name a file: ${!name}" >&2
        return 1
    fi
}

check_environment() {
    require_file CONTROLLER_DISK
    require_file WORKER_DISK
    require_file TEST_KUBECONFIG
    require_file SSH_KEY
    require_variable QEMU_SYSTEM
    require_variable QEMU_MACHINE_ARGS
    require_variable NEW_IMAGE
    require_variable LAKE_ENDPOINT
    command -v "$QEMU_SYSTEM" >/dev/null
    command -v kubectl >/dev/null
    command -v jq >/dev/null
    command -v curl >/dev/null
    command -v ssh >/dev/null
}

wait_for_json() {
    local expression=$1
    local expected=$2
    local attempts=${3:-120}
    local value
    for ((attempt = 0; attempt < attempts; attempt++)); do
        value=$(kubectl --kubeconfig "$TEST_KUBECONFIG" get nodeimage node-image-test -o json 2>/dev/null | jq -r "$expression" || true)
        if [[ $value == "$expected" ]]; then
            return 0
        fi
        sleep 2
    done
    echo "timed out waiting for $expression to equal $expected; last value: $value" >&2
    return 1
}

start_vm() {
    local disk=$1
    local api_forward=$2
    local ssh_forward=$3
    local serial_log=$4
    local -a machine_args
    read -r -a machine_args <<<"$QEMU_MACHINE_ARGS"
    "$QEMU_SYSTEM" \
        "${machine_args[@]}" \
        -m 4096 \
        -smp 2 \
        -snapshot \
        -drive "file=$disk,if=virtio,format=qcow2" \
        -nic "user,hostfwd=tcp:127.0.0.1:$api_forward-:6443,hostfwd=tcp:127.0.0.1:$ssh_forward-:22" \
        -display none \
        -serial "file:$serial_log" &
    echo $!
}

cleanup() {
    if [[ -n ${controller_pid:-} ]]; then
        kill "$controller_pid" 2>/dev/null || true
    fi
    if [[ -n ${worker_pid:-} ]]; then
        kill -CONT "$worker_pid" 2>/dev/null || true
        kill "$worker_pid" 2>/dev/null || true
    fi
}

check_environment
if [[ ${1:-} == --check ]]; then
    echo "immutable-node QEMU environment is complete"
    exit 0
fi

test_dir=$(mktemp -d "${TMPDIR:-/tmp}/k0s-node-image-test.XXXXXX")
transcript=$test_dir/transcript.log
exec > >(tee "$transcript") 2>&1
trap cleanup EXIT HUP INT TERM

controller_pid=$(start_vm "$CONTROLLER_DISK" 6443 2222 "$test_dir/controller.serial.log")
worker_pid=$(start_vm "$WORKER_DISK" 16443 2223 "$test_dir/worker.serial.log")

kubectl --kubeconfig "$TEST_KUBECONFIG" wait --for=condition=Ready nodes --all --timeout=5m
kubectl --kubeconfig "$TEST_KUBECONFIG" label node --all liquid.orama.dev/node-image=managed --overwrite

controller_name=$(kubectl --kubeconfig "$TEST_KUBECONFIG" get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].metadata.name}')
worker_name=$(kubectl --kubeconfig "$TEST_KUBECONFIG" get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[0].metadata.name}')

kubectl --kubeconfig "$TEST_KUBECONFIG" apply -f - <<EOF
apiVersion: liquid.k0sproject.io/v1alpha1
kind: NodeImage
metadata:
  name: node-image-test
spec:
  digest: $NEW_IMAGE
  selector:
    matchLabels:
      liquid.orama.dev/node-image: managed
  strategy:
    deadline: 30s
    automaticRollback: true
EOF

wait_for_json '.status.targets[0].name' "$controller_name"
wait_for_json '.status.targets[1].name' "$worker_name"
wait_for_json '.status.targets[0].state' Healthy 180
kill -STOP "$worker_pid"
wait_for_json '.status.targets[1].state' RollingBack 60
kill -CONT "$worker_pid"
wait_for_json '.status.targets[1].state' RolledBack 180
wait_for_json '.status.phase' Failed

principal=qemu-operator
certificate=$test_dir/operator-cert.pub
curl --fail --silent --show-error \
    --request POST \
    --header 'Content-Type: application/json' \
    --data "{\"principal\":\"$principal\",\"ttl_seconds\":60}" \
    "$LAKE_ENDPOINT/v1/pki/issue_ssh_user_certificate" \
    | jq -r .certificate >"$certificate"

kubectl --kubeconfig "$TEST_KUBECONFIG" apply -f - <<EOF
apiVersion: liquid.k0sproject.io/v1alpha1
kind: BreakGlass
metadata:
  name: qemu-login
spec:
  node: $controller_name
  principal: $principal
  reason: "qemu immutable-node integration test"
  ttlSeconds: 60
EOF

kubectl --kubeconfig "$TEST_KUBECONFIG" wait --for=jsonpath='{.status.phase}'=Open breakglass/qemu-login --timeout=2m
ssh -F none \
    -i "$SSH_KEY" \
    -o "CertificateFile=$certificate" \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -p 2222 \
    root@127.0.0.1 </dev/null
kubectl --kubeconfig "$TEST_KUBECONFIG" wait --for=jsonpath='{.status.phase}'=Closed breakglass/qemu-login --timeout=3m
if ssh -F none -o ConnectTimeout=5 -p 2222 root@127.0.0.1 true; then
    echo "sshd remained available after the access window" >&2
    exit 1
fi

events=$(curl --fail --silent --show-error "$LAKE_ENDPOINT/v1/audit/events?request=qemu-login")
for event_type in break_glass_open break_glass_login break_glass_session_close break_glass_close; do
    jq --exit-status --arg event_type "$event_type" 'any(.events[]; .type == $event_type)' <<<"$events" >/dev/null
done

echo "immutable-node QEMU integration test passed"
echo "artifacts: $test_dir"
