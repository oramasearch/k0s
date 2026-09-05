#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC1091,SC2154

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$script_dir/lib.sh"

usage() {
  printf '%s\n' "usage: $0 <node>"
}

operator_controller_for() {
  local excluded="$1"
  local controller

  for controller in "${lifecycle_controllers[@]}"; do
    if [ "$controller" != "$excluded" ]; then
      printf '%s\n' "$controller"
      return 0
    fi
  done
  lifecycle_die "no remaining controller can replace $excluded"
}

prepare_replacement_host() {
  local node="$1"

  lifecycle_ssh_run "$node" bash -ceu '
    /usr/local/bin/k0s stop || true
    /usr/local/bin/k0s reset || true
  '
}

wait_for_worker() {
  local controller="$1"
  local worker="$2"

  lifecycle_k0s "$controller" kc get node "$worker" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -qx True
}

wait_for_controller_member() {
  local controller="$1"
  local joining="$2"

  lifecycle_k0s "$controller" kc get etcdmember "$joining" -o jsonpath='{.status.conditions[?(@.type=="Joined")].status}' | grep -qx True
}

wait_for_storage_replicas() {
  local controller="$1"
  local robustness

  robustness="$(lifecycle_k0s "$controller" kc -n longhorn-system get volumes.longhorn.io -o jsonpath='{range .items[*]}{.status.robustness}{"\\n"}{end}' 2>/dev/null || true)"
  [ -z "$robustness" ] && return 0
  printf '%s\n' "$robustness" | awk '$1 != "healthy" { exit 1 }'
}

main() {
  local node="${1:-}"
  local role
  local operator
  local token
  local config

  [ "$#" -eq 1 ] || { usage >&2; exit 2; }
  lifecycle_load_inventory
  lifecycle_ssh_args
  lifecycle_scp_args
  role="$(lifecycle_role_for "$node")"
  operator="$(operator_controller_for "$node")"
  lifecycle_wait_for_api "$operator"

  case "$role" in
    worker)
      lifecycle_k0s "$operator" kc drain "$node" --ignore-daemonsets --delete-emptydir-data --timeout="${K0S_LIFECYCLE_DRAIN_TIMEOUT:-10m}"
      lifecycle_k0s "$operator" kc delete node "$node" --ignore-not-found
      prepare_replacement_host "$node"
      token="$(lifecycle_k0s "$operator" token create --role worker --expiry="${K0S_LIFECYCLE_TOKEN_EXPIRY:-1h}")"
      printf '%s' "$token" | lifecycle_ssh_pipe "$node" install -m 0600 /dev/stdin /tmp/k0s-worker.token
      lifecycle_worker_args
      lifecycle_k0s "$node" install worker --force --token-file=/tmp/k0s-worker.token "${lifecycle_install_args[@]}"
      lifecycle_k0s "$node" start
      lifecycle_wait_for "worker $node" wait_for_worker "$operator" "$node"
      ;;
    controller)
      [ "${#lifecycle_controllers[@]}" -ge 3 ] || lifecycle_die "controller replacement requires at least three configured controllers"
      lifecycle_k0s "$operator" kc delete controlnode.autopilot.k0sproject.io "$node" --ignore-not-found
      lifecycle_k0s "$operator" kc patch etcdmember "$node" --type merge -p '{"spec":{"leave":true}}'
      lifecycle_k0s "$operator" kc wait etcdmember "$node" --for condition=Joined=False --timeout="${K0S_LIFECYCLE_ETCD_TIMEOUT:-10m}"
      prepare_replacement_host "$node"
      config="$(mktemp)"
      trap 'rm -f -- "$config"' EXIT
      lifecycle_ssh_run "$operator" cat /etc/k0s/k0s.yaml >"$config"
      lifecycle_ssh_run "$node" mkdir -p /etc/k0s
      lifecycle_scp_to "$config" "$node" /etc/k0s/k0s.yaml
      token="$(lifecycle_k0s "$operator" token create --role controller --expiry="${K0S_LIFECYCLE_TOKEN_EXPIRY:-1h}")"
      printf '%s' "$token" | lifecycle_ssh_pipe "$node" install -m 0600 /dev/stdin /tmp/k0s-controller.token
      lifecycle_controller_args
      lifecycle_k0s "$node" install controller --force --config=/etc/k0s/k0s.yaml --token-file=/tmp/k0s-controller.token "${lifecycle_install_args[@]}"
      lifecycle_k0s "$node" start
      lifecycle_wait_for_api "$node"
      lifecycle_wait_for "etcd member $node" wait_for_controller_member "$operator" "$node"
      ;;
  esac

  lifecycle_wait_for "storage replicas" wait_for_storage_replicas "$operator"
  lifecycle_storage_hook K0S_LIFECYCLE_STORAGE_RESCHEDULE_COMMAND "$operator"
  printf '%s\n' "replacement complete for $node"
}

main "$@"
