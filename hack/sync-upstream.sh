#!/usr/bin/env bash
# Synchronize this private k0s copy with k0sproject/k0s.

set -euo pipefail

readonly expected_upstream_url='https://github.com/k0sproject/k0s.git'
readonly disabled_push_url='DISABLED'
readonly last_synced_prefix='**Last synced upstream tag:**'

dry_run=false
push=false
requested_tag=''

usage() {
  cat <<'EOF'
Usage: hack/sync-upstream.sh [--dry-run] [--tag <tag>] [--push]

Fetch k0sproject/k0s, fast-forward main, rebase liquid, and record the upstream
release tag in ORAMA-FORK.md.

  --dry-run    Print the planned sync without changing local branches or pushing.
  --tag <tag>  Sync main to this upstream release tag instead of upstream/main.
  --push       Push main fast-forward only and liquid with --force-with-lease.
EOF
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_clean_worktree() {
  local worktree=$1

  if test -n "$(git -C "$worktree" status --porcelain)"; then
    die "worktree is not clean: $worktree"
  fi
}

branch_worktree() {
  local branch=$1
  local worktree=''
  local branch_ref=''
  local line=''

  while IFS= read -r line; do
    case $line in
      worktree\ *) worktree=${line#worktree } ;;
      branch\ *)
        branch_ref=${line#branch }
        if test "$branch_ref" = "refs/heads/$branch"; then
          printf '%s\n' "$worktree"
          return 0
        fi
        ;;
    esac
  done < <(git worktree list --porcelain)

  return 1
}

latest_upstream_tag() {
  git for-each-ref --sort=-version:refname --format='%(refname:strip=2)' refs/tags | head -n 1
}

latest_remote_upstream_tag() {
  git ls-remote --tags --refs upstream |
    awk '{sub("refs/tags/", "", $2); print $2}' |
    sort -V |
    tail -n 1
}

remote_ref_commit() {
  local ref=$1

  git ls-remote upstream "$ref" | awk 'NR == 1 { print $1 }'
}

remote_tag_commit() {
  local tag=$1
  local direct_commit=''
  local peeled_commit=''

  direct_commit=$(remote_ref_commit "refs/tags/$tag")
  peeled_commit=$(remote_ref_commit "refs/tags/$tag^{}")

  if test -n "$peeled_commit"; then
    printf '%s\n' "$peeled_commit"
  else
    printf '%s\n' "$direct_commit"
  fi
}

update_last_synced_tag() {
  local file=$1
  local tag=$2
  local temporary_file="${file}.tmp.$$"

  awk -v replacement="$last_synced_prefix \`$tag\`" '
    $0 ~ /^\*\*Last synced upstream tag:\*\*/ {
      print replacement
      replaced = 1
      next
    }
    { print }
    END {
      if (!replaced) {
        exit 1
      }
    }
  ' "$file" > "$temporary_file" || {
    rm -f "$temporary_file"
    die "ORAMA-FORK.md is missing the last-synced marker"
  }

  mv "$temporary_file" "$file"
}

while test "$#" -gt 0; do
  case $1 in
    --dry-run)
      dry_run=true
      ;;
    --push)
      push=true
      ;;
    --tag)
      test "$#" -ge 2 || die '--tag requires a tag name'
      requested_tag=$2
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
  shift
done

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || die 'run this inside a Git checkout'
cd "$repo_root"

git remote get-url upstream >/dev/null 2>&1 || die 'missing required upstream remote'
upstream_url=$(git remote get-url upstream)
upstream_push_url=$(git remote get-url --push upstream)

test "$upstream_url" = "$expected_upstream_url" ||
  die "upstream fetch URL must be $expected_upstream_url; found $upstream_url"
test "$upstream_push_url" = "$disabled_push_url" ||
  die "upstream push URL must be $disabled_push_url; found $upstream_push_url"

if test -f ORAMA-FORK.md; then
  grep -Fq 'modified to support' ORAMA-FORK.md ||
    die 'ORAMA-FORK.md must state that this copy is modified'
  grep -Fq "$last_synced_prefix" ORAMA-FORK.md ||
    die 'ORAMA-FORK.md is missing the last-synced marker'
else
  if git show-ref --verify --quiet refs/heads/liquid; then
    liquid_policy_ref=liquid
  else
    liquid_policy_ref=origin/liquid
  fi
  git cat-file -e "$liquid_policy_ref:ORAMA-FORK.md" 2>/dev/null ||
    die 'liquid must contain ORAMA-FORK.md at the repository root'
  git show "$liquid_policy_ref:ORAMA-FORK.md" | grep -Fq 'modified to support' ||
    die 'ORAMA-FORK.md must state that this copy is modified'
  git show "$liquid_policy_ref:ORAMA-FORK.md" | grep -Fq "$last_synced_prefix" ||
    die 'ORAMA-FORK.md is missing the last-synced marker'
fi

if test -n "$requested_tag"; then
  git check-ref-format "refs/tags/$requested_tag" || die "invalid tag name: $requested_tag"
  target_description="upstream tag $requested_tag"
else
  target_description='upstream/main'
fi

if "$dry_run"; then
  if test -n "$requested_tag"; then
    target_commit=$(remote_tag_commit "$requested_tag")
    test -n "$target_commit" || die "upstream tag does not exist: $requested_tag"
    synced_tag=$requested_tag
  else
    target_commit=$(remote_ref_commit refs/heads/main)
    test -n "$target_commit" || die 'upstream/main does not exist'
    synced_tag=$(latest_remote_upstream_tag)
    test -n "$synced_tag" || die 'could not determine the latest upstream tag'
  fi

  printf 'Dry run: no local branches will change and nothing will be pushed.\n'
  printf 'Would run: git fetch upstream --tags\n'
  printf 'Current upstream tag: %s\n' "$synced_tag"
  printf 'Would fast-forward main from %s to %s (%s).\n' \
    "$(git rev-parse --short main)" "${target_commit:0:12}" "$target_description"
  printf 'Would rebase liquid onto the resulting main.\n'
  printf 'Would verify LICENSE and, when supplied upstream, NOTICE after the rebase.\n'
  printf 'Would record %s in ORAMA-FORK.md.\n' "$synced_tag"
  if "$push"; then
    printf 'Would push main fast-forward only and liquid with --force-with-lease.\n'
  fi
  exit 0
fi

printf 'Fetching upstream tags and branches.\n'
git fetch upstream --tags

if ! git show-ref --verify --quiet refs/heads/liquid; then
  git show-ref --verify --quiet refs/remotes/origin/liquid ||
    die 'missing local liquid branch and origin/liquid fallback'
  git branch --track liquid origin/liquid
fi

if test -n "$requested_tag"; then
  target_commit=$(git rev-parse --verify "refs/tags/$requested_tag^{commit}") ||
    die "upstream tag does not resolve to a commit: $requested_tag"
  synced_tag=$requested_tag
else
  target_commit=$(git rev-parse --verify upstream/main)
  synced_tag=$(latest_upstream_tag)
  test -n "$synced_tag" || die 'could not determine the latest upstream tag'
fi

git cat-file -e "$target_commit:LICENSE" 2>/dev/null ||
  die "$target_description does not contain LICENSE at its root"
if git cat-file -e "$target_commit:NOTICE" 2>/dev/null; then
  upstream_has_notice=true
  printf 'Apache check: %s contains LICENSE and NOTICE.\n' "$target_description"
else
  upstream_has_notice=false
  printf 'Apache check: %s contains LICENSE; it contains no NOTICE, so no NOTICE check applies.\n' \
    "$target_description"
fi

main_commit=$(git rev-parse --verify main)
if ! git merge-base --is-ancestor "$main_commit" "$target_commit"; then
  die "refusing to move main: $target_description is not a fast-forward from main"
fi

liquid_worktree=$(branch_worktree liquid || true)
if test -n "$liquid_worktree"; then
  require_clean_worktree "$liquid_worktree"
fi

main_worktree=$(branch_worktree main || true)
if test -n "$main_worktree"; then
  require_clean_worktree "$main_worktree"
  printf 'Fast-forwarding main in %s.\n' "$main_worktree"
  git -C "$main_worktree" merge --ff-only "$target_commit"
else
  printf 'Fast-forwarding main.\n'
  git update-ref refs/heads/main "$target_commit" "$main_commit"
fi

temporary_liquid_worktree=''
if test -z "$liquid_worktree"; then
  temporary_liquid_worktree=$(mktemp -d "${TMPDIR:-/tmp}/k0s-upstream-sync.XXXXXX")
  git worktree add "$temporary_liquid_worktree" liquid
  liquid_worktree=$temporary_liquid_worktree
fi

printf 'Rebasing liquid onto main.\n'
if ! git -C "$liquid_worktree" rebase main; then
  printf '\nRebase stopped because of conflicts. Resolve them in: %s\n' "$liquid_worktree" >&2
  printf '  cd %q\n' "$liquid_worktree" >&2
  printf '  git status\n' >&2
  printf '  # resolve the conflicted files, then stage them\n' >&2
  printf '  git add <resolved-files>\n' >&2
  printf '  git rebase --continue\n' >&2
  printf '  # repeat the resolution commands until the rebase completes\n' >&2
  if "$push"; then
    printf '  %q --push\n' "$repo_root/hack/sync-upstream.sh" >&2
  else
    printf '  %q\n' "$repo_root/hack/sync-upstream.sh" >&2
  fi
  if test -n "$temporary_liquid_worktree"; then
    printf '  git worktree remove %q\n' "$liquid_worktree" >&2
  fi
  exit 1
fi

git -C "$liquid_worktree" cat-file -e 'HEAD:ORAMA-FORK.md' 2>/dev/null ||
  die 'liquid must retain ORAMA-FORK.md after the rebase'
git -C "$liquid_worktree" show 'HEAD:ORAMA-FORK.md' | grep -Fq 'modified to support' ||
  die 'ORAMA-FORK.md must state that this copy is modified after the rebase'
git -C "$liquid_worktree" cat-file -e 'HEAD:LICENSE' 2>/dev/null ||
  die 'liquid must retain LICENSE at the repository root after the rebase'
if "$upstream_has_notice"; then
  git -C "$liquid_worktree" cat-file -e 'HEAD:NOTICE' 2>/dev/null ||
    die 'liquid must retain the upstream NOTICE at the repository root after the rebase'
  notice_suffix=' and NOTICE'
else
  notice_suffix=''
fi
printf 'Apache check: liquid retains LICENSE%s after the rebase.\n' \
  "$notice_suffix"

update_last_synced_tag "$liquid_worktree/ORAMA-FORK.md" "$synced_tag"
git -C "$liquid_worktree" add ORAMA-FORK.md
git -C "$liquid_worktree" commit -m "Record upstream sync $synced_tag"

if "$push"; then
  printf 'Pushing main fast-forward only.\n'
  git push origin refs/heads/main:refs/heads/main
  printf 'Pushing liquid with --force-with-lease.\n'
  git push --force-with-lease origin refs/heads/liquid:refs/heads/liquid
fi

if test -n "$temporary_liquid_worktree"; then
  git worktree remove "$temporary_liquid_worktree"
fi

printf 'Sync complete: main is at %s and liquid is rebased; recorded %s.\n' \
  "$(git rev-parse --short main)" "$synced_tag"
