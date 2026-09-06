#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# shellcheck disable=SC1091

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$script_dir/lib.sh"

usage() {
  printf '%s\n' "usage: $0 <backup-archive>"
}

main() {
  local archive="${1:-}"
  local controller

  [ "$#" -eq 1 ] || { usage >&2; exit 2; }
  lifecycle_load_inventory
  lifecycle_ssh_args
  controller="$(lifecycle_primary_controller)"
  lifecycle_require_storage_backup_hook "$controller"
  lifecycle_storage_hook K0S_LIFECYCLE_STORAGE_BACKUP_COMMAND "$controller"

  mkdir -p -- "$(dirname -- "$archive")"
  lifecycle_k0s_pipe "$controller" backup --save-path - >"$archive"
  [ -s "$archive" ] || lifecycle_die "k0s backup produced an empty archive"
  printf '%s\n' "backup written to $archive"
}

main "$@"
