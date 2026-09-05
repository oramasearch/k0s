#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC1091,SC2154,SC2016

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$script_dir/lib.sh"

archive_kubernetes_credentials() {
  local node="$1"
  local worker="$2"
  local rotation_dir=/var/lib/k0s/lifecycle-certificate-rotation

  lifecycle_ssh_run "$node" bash -ceu '
    rotation_dir="$1"
    worker="$2"
    mkdir -p "$rotation_dir"
    for credential in ca.crt ca.key sa.crt sa.key sa.pub; do
      path="/var/lib/k0s/pki/$credential"
      [ ! -e "$path" ] || mv "$path" "$rotation_dir/$credential"
    done
    if [ "$worker" = true ]; then
      for path in /var/lib/k0s/kubelet.conf /var/lib/k0s/kubelet/pki; do
        [ ! -e "$path" ] || mv "$path" "$rotation_dir/$(basename "$path")"
      done
    fi
  ' bash "$rotation_dir" "$worker"
}

stop_node() {
  local node="$1"

  lifecycle_ssh_run "$node" bash -ceu "$lifecycle_k0s stop || true"
}

start_node() {
  local node="$1"

  lifecycle_k0s "$node" start
}

install_ca_bundle() {
  local node="$1"
  local bundle="$2"

  lifecycle_scp_to "$bundle" "$node" /tmp/k0s-ca-bundle.tar.gz
  lifecycle_ssh_run "$node" tar -C /var/lib/k0s/pki -xzf /tmp/k0s-ca-bundle.tar.gz
}

write_bootstrap_config() {
  local node="$1"
  local bootstrap="$2"

  lifecycle_scp_to "$bootstrap" "$node" /var/lib/k0s/kubelet-bootstrap.conf
}

wait_for_worker() {
  local controller="$1"
  local worker="$2"

  lifecycle_k0s "$controller" kc get node "$worker" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -qx True
}

main() {
  local primary
  local node
  local bundle
  local bootstrap
  local worker

  [ "$#" -eq 0 ] || { printf '%s\n' "usage: $0" >&2; exit 2; }
  [ -s "${K0S_LIFECYCLE_CA_ROTATION_BACKUP:-}" ] || lifecycle_die "set K0S_LIFECYCLE_CA_ROTATION_BACKUP to a verified backup archive before rotating the Kubernetes CA"
  lifecycle_load_inventory
  lifecycle_ssh_args
  lifecycle_scp_args
  primary="$(lifecycle_primary_controller)"

  for node in "${lifecycle_workers[@]}"; do
    stop_node "$node"
  done
  for node in "${lifecycle_controllers[@]}"; do
    stop_node "$node"
  done
  for node in "${lifecycle_controllers[@]}"; do
    archive_kubernetes_credentials "$node" false
  done
  for node in "${lifecycle_workers[@]}"; do
    archive_kubernetes_credentials "$node" true
  done

  start_node "$primary"
  lifecycle_wait_for_api "$primary"

  bundle="$(mktemp)"
  bootstrap="$(mktemp)"
  trap 'rm -f -- "$bundle" "$bootstrap"' EXIT
  lifecycle_ssh_run "$primary" tar -C /var/lib/k0s/pki -czf - ca.crt ca.key sa.pub sa.key >"$bundle"
  lifecycle_k0s_pipe "$primary" token create --role worker --expiry="${K0S_LIFECYCLE_TOKEN_EXPIRY:-1h}" | base64 -d | gzip -d >"$bootstrap"

  for node in "${lifecycle_controllers[@]}"; do
    [ "$node" = "$primary" ] && continue
    install_ca_bundle "$node" "$bundle"
    start_node "$node"
    lifecycle_wait_for_api "$node"
  done
  for worker in "${lifecycle_workers[@]}"; do
    write_bootstrap_config "$worker" "$bootstrap"
    start_node "$worker"
    lifecycle_wait_for "worker $worker" wait_for_worker "$primary" "$worker"
    lifecycle_ssh_run "$worker" rm -f /var/lib/k0s/kubelet-bootstrap.conf
  done

  "$script_dir/check-health.sh"
  printf '%s\n' "Kubernetes CA and serving certificates rotated"
}

main "$@"
