#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC1091,SC2154,SC2016

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$script_dir/lib.sh"

check_node_ready() {
  local controller="$1"
  local node="$2"

  lifecycle_k0s "$controller" kc get node "$node" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -qx True
}

check_etcd_members() {
  local controller="$1"
  local output
  local expected

  output="$(lifecycle_k0s "$controller" etcd member-list)"
  for expected in "${lifecycle_controllers[@]}"; do
    printf '%s\n' "$output" | grep -Fq "\"$expected\"" || return 1
  done
}

check_certificate_expiry() {
  local node="$1"
  local role="$2"
  local threshold="${K0S_LIFECYCLE_CERTIFICATE_WARNING_SECONDS:-2592000}"

  lifecycle_ssh_run "$node" bash -ceu '
    threshold="$1"
    role="$2"
    found=false
    if [ "$role" = controller ]; then
      search_path=/var/lib/k0s/pki
      suffix="*.crt"
    else
      search_path=/var/lib/k0s/kubelet/pki
      suffix="*.pem"
    fi
    if [ -d "$search_path" ]; then
      while IFS= read -r -d "" certificate; do
        found=true
        openssl x509 -checkend "$threshold" -noout -in "$certificate"
      done < <(find "$search_path" -type f -name "$suffix" -print0)
    fi
    "$found"
  ' bash "$threshold" "$role"
}

check_component_versions() {
  local controller="$1"

  lifecycle_k0s "$controller" liquid components | awk '
    NR == 1 { next }
    $5 != "deployed" && $5 != "external" && $5 != "disabled" { exit 1 }
  '
}

check_pending_plan() {
  local controller="$1"
  local state

  state="$(lifecycle_k0s "$controller" kc get plan autopilot -o jsonpath='{.status.state}' 2>/dev/null || true)"
  [ -z "$state" ] || [ "$state" = "Completed" ]
}

main() {
  local controller
  local node

  [ "$#" -eq 0 ] || { printf '%s\n' "usage: $0" >&2; exit 2; }
  lifecycle_load_inventory
  lifecycle_ssh_args
  controller="$(lifecycle_primary_controller)"
  lifecycle_wait_for_api "$controller"

  for node in "${lifecycle_workers[@]}"; do
    check_node_ready "$controller" "$node"
  done
  check_etcd_members "$controller"
  for node in "${lifecycle_controllers[@]}"; do
    check_certificate_expiry "$node" controller
  done
  for node in "${lifecycle_workers[@]}"; do
    check_certificate_expiry "$node" worker
  done
  check_component_versions "$controller"
  check_pending_plan "$controller"
  printf '%s\n' "cluster health checks passed"
}

main "$@"
