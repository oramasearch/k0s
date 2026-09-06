#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
readonly installed_k0s="/usr/local/bin/k0s"

fail() {
  printf 'lake-on-k0s teardown: %s\n' "$*" >&2
  exit 1
}

check_environment() {
  [ "$(uname -s)" = Linux ] || fail "this teardown must run on Linux"
  [ "${EUID:-$(id -u)}" -eq 0 ] || fail "run this teardown as root"
  [ -x "$installed_k0s" ] || fail "k0s is not installed at $installed_k0s"
  command -v helm >/dev/null 2>&1 || fail "required command not found: helm"
}

remove_workload() {
  export KUBECONFIG=/var/lib/k0s/pki/admin.conf

  "$installed_k0s" kubectl delete -f "$script_dir/route.yaml" --ignore-not-found
  "$installed_k0s" kubectl delete lake hello --ignore-not-found
  helm uninstall orama-operator --namespace orama-system --ignore-not-found
  helm uninstall cloudnative-pg --namespace cnpg-system --ignore-not-found
  "$installed_k0s" kubectl delete namespace lake-dev orama-system cnpg-system --ignore-not-found
}

reset_k0s() {
  "$installed_k0s" stop
  "$installed_k0s" reset
  rm -f -- "$installed_k0s"
  printf '%s\n' "k0s was reset. Reboot this host to clear the remaining network rules."
}

main() {
  [ "$#" -eq 0 ] || fail "usage: $0"
  check_environment
  remove_workload

  if [ "${KEEP_K0S:-0}" = 1 ]; then
    printf '%s\n' "Application resources were removed; KEEP_K0S=1 left the cluster installed."
    return
  fi
  reset_k0s
}

main "$@"
