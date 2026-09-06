#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC1091,SC2154

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$script_dir/lib.sh"

usage() {
  printf '%s\n' "usage: $0 <version>"
}

platform_for_node() {
  local node="$1"
  local machine

  machine="$(lifecycle_ssh_run "$node" uname -m)"
  case "$machine" in
    x86_64) printf '%s\n' "linux-amd64" ;;
    aarch64|arm64) printf '%s\n' "linux-arm64" ;;
    armv7l|armv6l) printf '%s\n' "linux-arm" ;;
    *) lifecycle_die "unsupported architecture on $node: $machine" ;;
  esac
}

binary_url_for_platform() {
  local version="$1"
  local platform="$2"
  local arch="${platform#linux-}"
  local template

  if [ -n "${K0S_LIFECYCLE_BINARY_URL:-}" ]; then
    printf '%s\n' "$K0S_LIFECYCLE_BINARY_URL"
    return 0
  fi

  template="${K0S_LIFECYCLE_BINARY_URL_TEMPLATE:-https://github.com/k0sproject/k0s/releases/download/{version}/k0s-{version}-{arch}}"
  template="${template//\{version\}/$version}"
  template="${template//\{arch\}/$arch}"
  printf '%s\n' "$template"
}

plan_state() {
  local controller
  local state

  for controller in "$@"; do
    if ! state="$(lifecycle_k0s_timed "$controller" kc get plan autopilot -o jsonpath='{.status.state}' 2>/dev/null)"; then
      continue
    fi
    printf '%s\n' "$state"
    return 0
  done
  return 1
}

wait_for_plan() {
  local controller
  local state

  state="$(plan_state "$@" || true)"
  case "$state" in
    Completed) return 0 ;;
    ApplyFailed|IncompleteTargets|Restricted|Warning)
      for controller in "$@"; do
        lifecycle_k0s_timed "$controller" kc get plan autopilot -o yaml >&2 && break
      done
      lifecycle_die "autopilot plan finished in $state"
      ;;
    *) return 1 ;;
  esac
}

main() {
  local version="${1:-}"
  local controller
  local state
  local plan
  local node
  local platform
  local url
  local force_update="${K0S_LIFECYCLE_FORCE_UPDATE:-false}"
  declare -a platforms=()

  [ "$#" -eq 1 ] || { usage >&2; exit 2; }
  lifecycle_load_inventory
  lifecycle_ssh_args
  controller="$(lifecycle_primary_controller)"

  state="$(plan_state "${lifecycle_controllers[@]}" || true)"
  case "$state" in
    "") ;;
    Completed)
      lifecycle_k0s "$controller" kc delete plan autopilot
      ;;
    *) lifecycle_die "an autopilot plan is already active: $state" ;;
  esac

  plan="$(mktemp)"
  trap 'rm -f -- "${plan:-}"' EXIT
  {
    printf '%s\n' "apiVersion: autopilot.k0sproject.io/v1beta2"
    printf '%s\n' "kind: Plan"
    printf '%s\n' "metadata:"
    printf '%s\n' "  name: autopilot"
    printf '%s\n' "spec:"
    printf '%s\n' "  id: lifecycle-$version"
    printf '%s\n' "  timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '%s\n' "  commands:"
    printf '%s\n' "    - k0supdate:"
    printf '%s\n' "        version: $version"
    if [ "$force_update" = "true" ]; then
      printf '%s\n' "        forceupdate: true"
    fi
    printf '%s\n' "        platforms:"
    for node in "${lifecycle_controllers[@]}" "${lifecycle_workers[@]}"; do
      platform="$(platform_for_node "$node")"
      if [[ " ${platforms[*]:-} " == *" $platform "* ]]; then
        continue
      fi
      platforms+=("$platform")
      url="$(binary_url_for_platform "$version" "$platform")"
      printf '%s\n' "          $platform:"
      printf '%s\n' "            url: $url"
    done
    printf '%s\n' "        targets:"
    printf '%s\n' "          controllers:"
    printf '%s\n' "            discovery:"
    printf '%s\n' "              static:"
    printf '%s\n' "                nodes:"
    for node in "${lifecycle_controllers[@]}"; do
      printf '%s\n' "                  - $node"
    done
    printf '%s\n' "          workers:"
    printf '%s\n' "            limits:"
    printf '%s\n' "              concurrent: ${K0S_LIFECYCLE_WORKER_CONCURRENCY:-1}"
    printf '%s\n' "            discovery:"
    printf '%s\n' "              static:"
    printf '%s\n' "                nodes:"
    for node in "${lifecycle_workers[@]}"; do
      printf '%s\n' "                  - $node"
    done
  } >"$plan"

  lifecycle_k0s_pipe_timed "$lifecycle_timeout" "$plan" "$controller" kc apply -f -
  lifecycle_wait_for "the autopilot plan" wait_for_plan "${lifecycle_controllers[@]}"
  lifecycle_k0s "$controller" kc get plan autopilot -o yaml
}

main "$@"
