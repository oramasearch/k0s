#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC2034

set -euo pipefail

readonly lifecycle_k0s="${K0S_LIFECYCLE_K0S:-/usr/local/bin/k0s}"
readonly lifecycle_timeout="${K0S_LIFECYCLE_TIMEOUT:-900}"
readonly lifecycle_poll_interval="${K0S_LIFECYCLE_POLL_INTERVAL:-5}"
readonly lifecycle_poll_command_timeout="${K0S_LIFECYCLE_POLL_COMMAND_TIMEOUT:-60}"

declare -a lifecycle_records=()
declare -a lifecycle_controllers=()
declare -a lifecycle_workers=()
declare -a lifecycle_ssh=()
declare -a lifecycle_scp=()
declare -a lifecycle_install_args=()

lifecycle_die() {
  printf '%s\n' "$*" >&2
  exit 1
}

lifecycle_load_inventory() {
  local inventory
  local line
  local role
  local node
  local target
  local extra
  local record

  inventory="${K0S_LIFECYCLE_INVENTORY:-}"
  [ -n "$inventory" ] || lifecycle_die "K0S_LIFECYCLE_INVENTORY is required"
  [ -r "$inventory" ] || lifecycle_die "cannot read inventory: $inventory"

  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%%#*}"
    [ -n "${line//[[:space:]]/}" ] || continue
    read -r role node target extra <<<"$line"
    [ -z "${extra:-}" ] || lifecycle_die "inventory rows have exactly three fields: $line"
    case "$role" in
      controller|worker) ;;
      *) lifecycle_die "inventory role must be controller or worker: $role" ;;
    esac
    [[ "$node" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || lifecycle_die "invalid node name: $node"
    [ -n "${target:-}" ] || lifecycle_die "missing SSH target for $node"
    for record in "${lifecycle_records[@]:-}"; do
      IFS='|' read -r _ node_in_record _ <<<"$record"
      if [ "$node_in_record" = "$node" ]; then
        lifecycle_die "duplicate inventory node: $node"
      fi
    done
    lifecycle_records+=("$role|$node|$target")
    if [ "$role" = "controller" ]; then
      lifecycle_controllers+=("$node")
    else
      lifecycle_workers+=("$node")
    fi
  done <"$inventory"

  [ "${#lifecycle_controllers[@]}" -gt 0 ] || lifecycle_die "inventory has no controllers"
}

lifecycle_role_for() {
  local requested_node="$1"
  local record
  local role
  local node
  local target

  for record in "${lifecycle_records[@]}"; do
    IFS='|' read -r role node target <<<"$record"
    if [ "$node" = "$requested_node" ]; then
      printf '%s\n' "$role"
      return 0
    fi
  done
  lifecycle_die "node is not in the inventory: $requested_node"
}

lifecycle_target_for() {
  local requested_node="$1"
  local record
  local role
  local node
  local target

  for record in "${lifecycle_records[@]}"; do
    IFS='|' read -r role node target <<<"$record"
    if [ "$node" = "$requested_node" ]; then
      printf '%s\n' "$target"
      return 0
    fi
  done
  lifecycle_die "node is not in the inventory: $requested_node"
}

lifecycle_primary_controller() {
  local configured="${K0S_LIFECYCLE_CONTROLLER:-}"

  if [ -n "$configured" ]; then
    [ "$(lifecycle_role_for "$configured")" = "controller" ] || lifecycle_die "$configured is not a controller"
    printf '%s\n' "$configured"
    return 0
  fi
  printf '%s\n' "${lifecycle_controllers[0]}"
}

lifecycle_ssh_args() {
  local config="${K0S_LIFECYCLE_SSH_CONFIG:-}"

  lifecycle_ssh=(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new)
  if [ -n "$config" ]; then
    lifecycle_ssh+=(-F "$config")
  fi
}

lifecycle_scp_args() {
  local config="${K0S_LIFECYCLE_SSH_CONFIG:-}"

  lifecycle_scp=(scp -o BatchMode=yes -o StrictHostKeyChecking=accept-new)
  if [ -n "$config" ]; then
    lifecycle_scp+=(-F "$config")
  fi
}

lifecycle_remote_command() {
  local command=""
  local argument

  for argument in "$@"; do
    printf -v command '%s%q ' "$command" "$argument"
  done
  printf '%s\n' "$command"
}

lifecycle_ssh_run() {
  local node="$1"
  shift
  local target
  local command

  target="$(lifecycle_target_for "$node")"
  command="$(lifecycle_remote_command "$@")"
  "${lifecycle_ssh[@]}" "$target" "$command"
}

lifecycle_ssh_pipe() {
  local node="$1"
  shift
  local target
  local command

  target="$(lifecycle_target_for "$node")"
  command="$(lifecycle_remote_command "$@")"
  "${lifecycle_ssh[@]}" "$target" "$command"
}

lifecycle_ssh_pipe_timed() {
  local timeout="$1"
  local source="$2"
  local node="$3"
  local target
  local command
  local process
  local elapsed=0

  shift 3
  target="$(lifecycle_target_for "$node")"
  command="$(lifecycle_remote_command "$@")"
  "${lifecycle_ssh[@]}" "$target" "$command" <"$source" &
  process=$!
  while kill -0 "$process" 2>/dev/null; do
    if [ "$elapsed" -ge "$timeout" ]; then
      kill "$process" 2>/dev/null || true
      wait "$process" || true
      return 124
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  wait "$process"
}

lifecycle_ssh_run_timed() {
  local timeout="$1"
  local node
  local target
  local command
  local process
  local elapsed=0

  shift
  node="$1"
  shift
  target="$(lifecycle_target_for "$node")"
  command="$(lifecycle_remote_command "$@")"
  "${lifecycle_ssh[@]}" "$target" "$command" &
  process=$!
  while kill -0 "$process" 2>/dev/null; do
    if [ "$elapsed" -ge "$timeout" ]; then
      kill "$process" 2>/dev/null || true
      wait "$process" || true
      return 124
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  wait "$process"
}

lifecycle_scp_to() {
  local source="$1"
  local node="$2"
  local destination="$3"
  local target

  target="$(lifecycle_target_for "$node")"
  "${lifecycle_scp[@]}" "$source" "$target:$destination"
}

lifecycle_k0s() {
  local node="$1"
  shift

  lifecycle_ssh_run "$node" "$lifecycle_k0s" "$@"
}

lifecycle_k0s_pipe() {
  local node="$1"
  shift

  lifecycle_ssh_pipe "$node" "$lifecycle_k0s" "$@"
}

lifecycle_k0s_pipe_timed() {
  local timeout="$1"
  local source="$2"
  local node="$3"

  shift 3
  lifecycle_ssh_pipe_timed "$timeout" "$source" "$node" "$lifecycle_k0s" "$@"
}

lifecycle_k0s_timed() {
  local node="$1"
  shift

  lifecycle_ssh_run_timed "$lifecycle_poll_command_timeout" "$node" "$lifecycle_k0s" "$@"
}

lifecycle_wait_for() {
  local description="$1"
  shift
  local elapsed=0

  until "$@"; do
    if [ "$elapsed" -ge "$lifecycle_timeout" ]; then
      lifecycle_die "timed out waiting for $description"
    fi
    sleep "$lifecycle_poll_interval"
    elapsed=$((elapsed + lifecycle_poll_interval))
  done
}

lifecycle_wait_for_api() {
  local controller="$1"

  lifecycle_wait_for "the API on $controller" lifecycle_k0s_timed "$controller" kc get --raw=/readyz
}

lifecycle_controller_args() {
  local configured="${K0S_LIFECYCLE_CONTROLLER_ARGS:-}"

  lifecycle_install_args=()
  if [ -n "$configured" ]; then
    read -r -a lifecycle_install_args <<<"$configured"
  fi
}

lifecycle_worker_args() {
  local configured="${K0S_LIFECYCLE_WORKER_ARGS:-}"

  lifecycle_install_args=()
  if [ -n "$configured" ]; then
    read -r -a lifecycle_install_args <<<"$configured"
  fi
}

lifecycle_install() {
  local node="$1"
  shift

  if [ "${#lifecycle_install_args[@]}" -eq 0 ]; then
    lifecycle_k0s "$node" install "$@"
    return
  fi
  lifecycle_k0s "$node" install "$@" "${lifecycle_install_args[@]}"
}

lifecycle_storage_hook() {
  local variable="$1"
  local controller="$2"
  local command="${!variable:-}"

  [ -n "$command" ] || return 0
  lifecycle_ssh_run "$controller" bash -ceu "$command"
}

lifecycle_has_persistent_volumes() {
  local controller="$1"
  local output

  output="$(lifecycle_k0s "$controller" kc get persistentvolumeclaims --all-namespaces -o name)"
  [ -n "$output" ]
}

lifecycle_require_storage_backup_hook() {
  local controller="$1"

  if lifecycle_has_persistent_volumes "$controller"; then
    [ -n "${K0S_LIFECYCLE_STORAGE_BACKUP_COMMAND:-}" ] || lifecycle_die "persistent volumes exist; set K0S_LIFECYCLE_STORAGE_BACKUP_COMMAND"
  fi
}
