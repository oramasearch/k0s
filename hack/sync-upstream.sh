#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 k0s authors
# SPDX-License-Identifier: Apache-2.0
# Orama modification: validate and synchronize the private upstream mirror.

set -euo pipefail

readonly expected_upstream_url='https://github.com/k0sproject/k0s.git'
readonly tag_namespace='refs/sync-upstream/tags'
readonly sync_guide='docs/liquid/upstream-sync.md'
dry_run=false
requested_tag=''

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: ./sync.sh [--dry-run] [--tag <tag>]

Fetch upstream, validate the mirror and licences, fast-forward main, rebase
liquid, and commit the sync record on liquid. This script never pushes.

  --dry-run    Fetch and run preflight checks; leave branches and files untouched.
  --tag <tag>  Select an upstream release tag instead of upstream/main.

Run an actual sync from a clean, dedicated checkout of liquid. No other worktree
may have main checked out. Conflicts remain for human review; rerere is disabled.
EOF
}

reachable_tag() {
  git for-each-ref --merged "$1" --sort=-version:refname \
    --format='%(refname:strip=3)' "$tag_namespace" | sed -n '1p'
}

recorded_value() {
  local document=$1
  local label=$2

  printf '%s\n' "$document" |
    awk -v prefix="**Last synced upstream $label:** " '
      index($0, prefix) == 1 {
        count++
        value = substr($0, length(prefix) + 1)
      }
      END {
        if (count == 1 && value ~ /^`[^`]+`$/) {
          print substr(value, 2, length(value) - 2)
        }
      }
    '
}

check_preserved_file() {
  local reference=$1
  local candidate=$2
  local filename=$3
  local expected_blob
  local actual_blob

  expected_blob=$(git rev-parse --verify "$reference:$filename") ||
    die "$reference is missing $filename"
  actual_blob=$(git rev-parse --verify "$candidate:$filename" 2>/dev/null) ||
    die "$candidate must retain $filename"
  test "$expected_blob" = "$actual_blob" ||
    die "$candidate:$filename differs from $reference:$filename; human review required"
  printf 'Licence check: %s is present and byte-for-byte unchanged from %s (%s).\n' \
    "$filename" "$reference" "$actual_blob"
}

check_licences() {
  local reference=$1
  local candidate=$2
  local policy
  local changes

  check_preserved_file "$reference" "$candidate" LICENSE
  if git cat-file -e "$reference:NOTICE" 2>/dev/null; then
    check_preserved_file "$reference" "$candidate" NOTICE
  else
    printf 'Licence check: %s has no root NOTICE; no upstream root NOTICE to preserve.\n' "$reference"
  fi
  if git cat-file -e "$reference:docs/LICENSE" 2>/dev/null; then
    check_preserved_file "$reference" "$candidate" docs/LICENSE
  fi
  policy=$(git show "$candidate:ORAMA-FORK.md") || die 'liquid must retain ORAMA-FORK.md'
  changes=$(git show "$candidate:docs/liquid/README.md") || die 'liquid must retain its change index'
  [[ "$policy" == *'modified to support'* ]] || die 'ORAMA-FORK.md must state our modifications'
  [[ "$changes" == *'# Liquid-specific k0s changes'* ]] || die 'the liquid change index is missing its declaration'
  printf 'Licence check: ORAMA-FORK.md declares modifications; docs/liquid/README.md records the changes.\n'
  printf 'Human review: check prominent notices in modified files and notices shipped with distribution artifacts.\n'
}

check_record() {
  local policy
  local guide
  local recorded_commit
  local recorded_tag_commit

  policy=$(git show liquid:ORAMA-FORK.md) || die 'liquid must retain ORAMA-FORK.md'
  recorded_tag=$(recorded_value "$policy" tag)
  recorded_commit=$(recorded_value "$policy" commit)
  test -n "$recorded_tag" || die 'last synced upstream tag is missing or still a placeholder'
  [[ "$recorded_tag" != *$'\n'* ]] || die 'multiple last synced upstream tag records'
  [[ "$recorded_commit" =~ ^[0-9a-f]{40}$ ]] || die 'last synced upstream commit must be a full commit ID'
  git check-ref-format "$tag_namespace/$recorded_tag" || die 'invalid recorded upstream tag'
  recorded_tag_commit=$(git rev-parse --verify "$tag_namespace/$recorded_tag^{commit}") ||
    die 'recorded tag does not exist upstream'
  git merge-base --is-ancestor "$recorded_tag_commit" "$recorded_commit" ||
    die 'recorded tag is not an ancestor of the recorded upstream commit'
  git merge-base --is-ancestor "$recorded_commit" "$old_base" ||
    die 'recorded upstream commit is not an ancestor of the shared upstream base'
  guide=$(git show "liquid:$sync_guide") || die 'liquid must retain the upstream sync guide'
  test "$(recorded_value "$guide" tag)" = "$recorded_tag" ||
    die 'the guide and ORAMA-FORK.md must record the same upstream tag'
  test "$(recorded_value "$guide" commit)" = "$recorded_commit" ||
    die 'the guide and ORAMA-FORK.md must record the same upstream commit'
  printf 'Last synced upstream tag: %s (recorded commit %s).\n' "$recorded_tag" "$recorded_commit"
}

check_merge_attributes() {
  local reference=$1
  local filename
  local attribute
  local value

  git ls-tree -r -z --name-only "$reference" |
    git check-attr --source "$reference" -z --stdin merge |
    while IFS= read -r -d '' filename; do
      IFS= read -r -d '' attribute
      IFS= read -r -d '' value
      test "$attribute" = merge || die 'unexpected merge attribute output'
      case $value in
        unspecified | set | unset | text | binary) ;;
        *)
          die "$reference:$filename uses merge=$value; automatic conflict drivers require human review"
          ;;
      esac
    done
}

require_sync_checkout() {
  test "$(git symbolic-ref --quiet --short HEAD)" = liquid ||
    die 'run an actual sync from a dedicated checkout of liquid; use --dry-run to inspect'
  test -z "$(git status --porcelain)" || die 'liquid checkout must be clean'
  if git worktree list --porcelain | grep -Fx 'branch refs/heads/main' >/dev/null; then
    die 'main is checked out in another worktree; use a dedicated clone'
  fi
  if git config --get-regexp '^merge\..*\.driver$' >/dev/null; then
    die 'custom merge drivers require human review; use a dedicated clone without them'
  fi
}

update_record() {
  local filename=$1
  local temporary_file="${filename}.tmp.$$"

  awk -v tag="$synced_tag" -v commit="$target_commit" '
    /^\*\*Last synced upstream tag:\*\*/ {
      print "**Last synced upstream tag:** `" tag "`"
      tags++
      next
    }
    /^\*\*Last synced upstream commit:\*\*/ {
      print "**Last synced upstream commit:** `" commit "`"
      commits++
      next
    }
    { print }
    END {
      if (tags != 1 || commits != 1) {
        exit 1
      }
    }
  ' "$filename" >"$temporary_file" || {
    rm -f "$temporary_file"
    die "$filename must have exactly one tag and commit record"
  }
  if cmp -s "$filename" "$temporary_file"; then
    rm "$temporary_file"
  else
    mv "$temporary_file" "$filename"
  fi
}

while test "$#" -gt 0; do
  case $1 in
    --dry-run)
      dry_run=true
      ;;
    --tag)
      test "$#" -ge 2 || die '--tag requires a tag name'
      requested_tag=$2
      git check-ref-format "refs/tags/$requested_tag" || die 'invalid tag name'
      shift
      ;;
    --help | -h)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1 (this script never pushes)"
      ;;
  esac
  shift
done

repo_root=$(git rev-parse --show-toplevel) || die 'run inside a Git checkout'
cd "$repo_root"
test "$(git config --get-all remote.upstream.url)" = "$expected_upstream_url" ||
  die "upstream must have exactly one fetch URL: $expected_upstream_url"
test "$(git remote get-url --push --all upstream)" = DISABLED ||
  die 'all upstream pushes must be guarded by the single push URL DISABLED'
for branch in main liquid; do
  git show-ref --verify --quiet "refs/heads/$branch" || die "missing local $branch branch"
done
for operation in rebase-merge rebase-apply MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD; do
  test ! -e "$(git rev-parse --git-path "$operation")" ||
    die "unfinished $operation; finish or abort it before syncing"
done

if ! "$dry_run"; then
  require_sync_checkout
fi
printf 'Fetching upstream main and tags; nothing will be pushed.\n'
git fetch --no-tags --prune upstream \
  '+refs/heads/main:refs/remotes/upstream/main' \
  "+refs/tags/*:$tag_namespace/*"
target_ref=refs/remotes/upstream/main
if test -n "$requested_tag"; then
  target_ref="$tag_namespace/$requested_tag"
fi
target_commit=$(git rev-parse --verify "$target_ref^{commit}") || die 'upstream target is missing'
main_commit=$(git rev-parse refs/heads/main)
if ! git merge-base --is-ancestor "$main_commit" "$target_commit"; then
  git log --oneline "$target_commit..main" >&2
  die "main cannot fast-forward to $target_ref: someone has committed to the mirror, likely a merged Dependabot PR (or an older target was selected). main takes upstream commits only. Human repair required; no branches changed."
fi
printf 'Mirror check: main can fast-forward from %s to %s.\n' "$main_commit" "$target_commit"
old_base=$(git merge-base liquid "$target_commit") || die 'liquid and upstream have no shared history'
synced_tag=$(reachable_tag "$target_commit")
test -n "$synced_tag" || die 'no upstream tag is an ancestor of the selected target'
check_record
check_merge_attributes liquid
check_merge_attributes "$target_commit"
printf 'New upstream tag: %s (highest version reachable from %s).\n' "$synced_tag" "$target_commit"
printf 'Upstream commits missing from liquid: %s\n' "$(git rev-list --count "liquid..$target_commit")"
printf 'Commits liquid carries: %s\n' "$(git rev-list --count "$target_commit..liquid")"
git log --reverse --oneline "$target_commit..liquid"
check_licences "$old_base" liquid
git cat-file -e "$target_commit:LICENSE" || die 'new upstream target has no LICENSE'
if git cat-file -e "$target_commit:NOTICE" 2>/dev/null; then
  printf 'New upstream target includes NOTICE; its contents must survive the rebase.\n'
else
  printf 'New upstream target has no root NOTICE.\n'
fi

if "$dry_run"; then
  printf 'Dry run: preflight checks passed; no branches or files changed.\n'
  if test "$main_commit" = "$target_commit" && test "$old_base" = "$target_commit"; then
    printf 'Upstream has not moved relative to main and liquid; no fast-forward or rebase needed.\n'
  else
    printf 'Would fast-forward main, rebase liquid, verify licences against the new upstream tree, and record %s.\n' "$synced_tag"
    printf 'Human review: a dry run does not attempt the rebase or establish that it is conflict-free.\n'
  fi
  exit 0
fi

if test "$main_commit" != "$target_commit"; then
  git update-ref -m 'Fast-forward upstream mirror' refs/heads/main "$target_commit" "$main_commit"
  printf 'Fast-forwarded main to %s.\n' "$target_commit"
fi
if test "$old_base" != "$target_commit"; then
  printf 'Rebasing liquid onto main with cached conflict resolution disabled.\n'
  if ! git -c rerere.enabled=false -c rerere.autoupdate=false \
    -c core.hooksPath=/dev/null -c rebase.autoStash=false \
    rebase --no-autostash --no-update-refs --rebase-merges --onto "$target_commit" "$old_base"; then
    printf '\nRebase stopped; conflicting paths (if any):\n' >&2
    git diff --name-only --diff-filter=U >&2
    printf 'Human action in %s: inspect git status, then resolve and git rebase --continue, or git rebase --abort.\n' "$repo_root" >&2
    printf 'main remains fast-forwarded. No sync record was written; nothing was pushed.\n' >&2
    exit 1
  fi
fi
check_licences "$target_commit" liquid
update_record ORAMA-FORK.md
update_record "$sync_guide"
if git diff --quiet -- ORAMA-FORK.md "$sync_guide"; then
  if test "$main_commit" = "$target_commit" && test "$old_base" = "$target_commit"; then
    printf 'Upstream has not moved; sync record is current. Nothing to do.\n'
  else
    printf 'Sync complete: main and liquid include %s; sync record was already current.\n' "$target_commit"
  fi
else
  git add ORAMA-FORK.md "$sync_guide"
  git -c core.hooksPath=/dev/null commit -m "Record upstream sync $synced_tag at ${target_commit:0:9}"
  printf 'Sync complete: main and liquid include %s; recorded %s.\n' "$target_commit" "$synced_tag"
fi
printf 'Nothing was pushed. Review the local branch changes before publishing to origin.\n'
