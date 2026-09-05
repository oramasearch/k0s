#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC1091,SC2154

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$script_dir/lib.sh"

usage() {
  printf '%s\n' "usage: $0 <backup-archive>"
}

join_controller() {
  local controller="$1"
  local token="$2"
  local config="$3"

  lifecycle_ssh_run "$controller" mkdir -p /etc/k0s
  lifecycle_scp_to "$config" "$controller" /etc/k0s/k0s.yaml
  printf '%s' "$token" | lifecycle_ssh_pipe "$controller" install -m 0600 /dev/stdin /tmp/k0s-controller.token
  lifecycle_controller_args
  lifecycle_k0s "$controller" install controller --force --config=/etc/k0s/k0s.yaml --token-file=/tmp/k0s-controller.token "${lifecycle_install_args[@]}"
  lifecycle_k0s "$controller" start
  lifecycle_wait_for_api "$controller"
}

main() {
  local archive="${1:-}"
  local primary
  local remote_archive=/tmp/k0s-lifecycle-backup.tar.gz
  local config
  local token
  local controller

  [ "$#" -eq 1 ] || { usage >&2; exit 2; }
  [ -r "$archive" ] || lifecycle_die "cannot read backup archive: $archive"
  lifecycle_load_inventory
  lifecycle_ssh_args
  lifecycle_scp_args
  primary="$(lifecycle_primary_controller)"

  lifecycle_scp_to "$archive" "$primary" "$remote_archive"
  lifecycle_k0s "$primary" restore --config-out /etc/k0s/k0s.yaml "$remote_archive"
  lifecycle_controller_args
  lifecycle_k0s "$primary" install controller --force --config=/etc/k0s/k0s.yaml "${lifecycle_install_args[@]}"
  lifecycle_k0s "$primary" start
  lifecycle_wait_for_api "$primary"
  lifecycle_storage_hook K0S_LIFECYCLE_STORAGE_RESTORE_COMMAND "$primary"

  if [ "${K0S_LIFECYCLE_REJOIN_CONTROLLERS:-false}" != "true" ]; then
    printf '%s\n' "restored $primary; rejoin the remaining controllers or set K0S_LIFECYCLE_REJOIN_CONTROLLERS=true"
    return 0
  fi

  config="$(mktemp)"
  trap 'rm -f -- "${config:-}"' EXIT
  lifecycle_ssh_run "$primary" cat /etc/k0s/k0s.yaml >"$config"
  token="$(lifecycle_k0s "$primary" token create --role controller --expiry="${K0S_LIFECYCLE_TOKEN_EXPIRY:-1h}")"
  for controller in "${lifecycle_controllers[@]}"; do
    [ "$controller" = "$primary" ] && continue
    join_controller "$controller" "$token" "$config"
  done
  printf '%s\n' "restored $primary and rejoined the configured controllers"
}

main "$@"
