<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
Orama modification: sync integrity checks, baseline record, and verification.
-->

# Upstream sync

`oramasearch/k0s` is Orama's fork of `k0sproject/k0s`. `main` contains only
upstream commits; `liquid` contains our changes rebased onto that mirror.
[`sync.sh`](../../sync.sh) calls [`hack/sync-upstream.sh`](../../hack/sync-upstream.sh).
Both entry points support the same options and never push.

## Recorded baseline

**Last synced upstream tag:** `v1.37.0-alpha.1+k0s.0`

**Last synced upstream commit:** `444794a05883e34dcf796f695ac4d00220556fa3`

The script updates these two lines and the matching record in
[`ORAMA-FORK.md`](../../ORAMA-FORK.md) in the same commit on `liquid`. It rejects
placeholder tags, unknown upstream tags, and a recorded tag or commit that is
not an ancestor of the already integrated upstream base. Tags come from a
separate fetched upstream namespace, so a local release tag cannot impersonate
an upstream tag. The reported tag is the highest version reachable from the
selected upstream commit, including prereleases; it is not the newest tag on an
unrelated maintenance branch.

Verified on 2026-09-08: `origin/main` and `upstream/main` were both
`377b3d4fc93c58e986438460288d8191f3bb09db`. The shared upstream base of `liquid`
was the commit recorded above, 354 commits after the recorded tag. Upstream was
47 commits ahead of that base, 401 commits after the same tag. `origin/liquid`
at `847dcdeec` carried 18 of our commits before this change. The baseline records
existing ancestry; this change does not perform the 47-commit catch-up.

## Run and verify

Use a dedicated clone with local `main` tracking `origin/main` and local `liquid`
checked out. The script will not move another worktree's checked-out `main`,
switch your current branch, create worktrees, or repair a polluted local mirror.
In a fresh clone of the copy whose default branch is `liquid`, prepare it with:

```sh
git branch --track main origin/main
git remote add upstream https://github.com/k0sproject/k0s.git
git remote set-url --push upstream DISABLED
./sync.sh --dry-run
./sync.sh
```

Skip branch/remote creation commands when they already exist. Do not run a
real sync in a shared development worktree. An actual run requires a clean
`liquid` checkout with no unfinished Git operation. Git must support
`git check-attr --source` for attribute inspection. The script rejects custom merge drivers and
nonstandard merge attributes, including `merge=union`, in the candidate trees.
Review attribute changes within the replayed history as well.

`--dry-run` fetches upstream main and tags and validates mirror ancestry, the
recorded baseline, and current licence files. It changes fetched refs and
Git objects, but no local branch, tracked file, commit, or remote branch. It
reports missing upstream commits and lists our commits to replay.
It cannot establish whether the eventual rebase will conflict.

An actual run fast-forwards `main` with a compare-and-swap ref update after
checking ancestry. It rebases `liquid`, preserving our merge structure, onto
the fetched commit. Rebase keeps our delta visible for review; merging
upstream into `liquid` would accumulate repeated merge history. Cached conflict
resolutions (`rerere`), automatic stashing, hooks during rebase/record creation,
and automatic updates to other branch refs are disabled. No conflict resolutions
are supplied by the script.

After a successful rebase, it verifies licence files against the new upstream
tree, updates both sync records, and commits only changed records. A repeated
run with no upstream or record change reports `Nothing to do` and creates no
commit. It still fetches and repeats the integrity and licence checks.

To select a specific fetched upstream release, use `--tag vX.Y.Z+k0s.N` with
either mode. An older target that would rewind `main` is refused. The old
`--push` option has been removed: publishing always follows human review of the
local result, including our commits and any resolved conflicts. Only the
owner performing that separate sync should publish `main` by a normal
fast-forward push and `liquid` using an explicit expected-tip force-with-lease.
Never push to `upstream`. This issue's delivery pushes only its feature branch.

## Failure and recovery

If `main` cannot fast-forward, **someone has committed to the mirror**, likely by
merging a Dependabot PR (or the requested target is older than `main`). The script
prints commits outside the target's history and stops before moving any branch.
Do not merge around the failure. Investigate the commits and obtain an owner
repair decision, or use a fresh clone when only the local branch is stale after
an already approved remote repair.

If the rebase conflicts, the script prints conflicting paths and leaves the
rebase in place. `main` has already fast-forwarded, but no sync record has been
committed and nothing has been pushed. Inspect `git status`, review each change,
stage human resolutions, and run `git rebase --continue`, or use
`git rebase --abort`. Disable rerere while continuing manually if it is enabled
in your configuration. Run the script again after finishing or aborting.
An aborted rebase does not rewind `main`; retrying from that state is supported.

A licence or record failure stops the run and names the failed check. After a
post-rebase failure, review the local `liquid` result before fixing the problem
and rerunning; do not publish it. No automatic rollback or history repair occurs.

## Licence checks

Preflight compares `liquid` to its shared upstream base. After rebase, the
same checks compare it to the selected upstream target:

- `LICENSE` must exist and match the upstream Git blob byte-for-byte.
- When upstream has a root `NOTICE`, its complete contents must survive unchanged.
  When absent, the report explicitly says there is no upstream root NOTICE to
  preserve. A newly introduced upstream NOTICE is checked after the rebase.
- `docs/LICENSE`, when supplied upstream, must also match byte-for-byte.
- `ORAMA-FORK.md` must declare that the copy is modified, and
  `docs/liquid/README.md` must retain the durable liquid change index.

Checks print their reference commit and blob IDs. They establish repository
file preservation and the presence of a modification declaration, not complete
distribution compliance. Apache-2.0 section 4(b) also requires prominent notices
in modified files; the index and Git history alone do not establish that. Review
those notices and attributions carried in released binaries/documentation.
Documentation changes retain CC-BY-SA-4.0. Upstream currently has no root NOTICE;
third-party notices, including submodule notices, remain a packaging review duty.

## Actions, Dependabot, and cadence

Run a sync at every upstream k0s release and review a dry run weekly between
releases. Dependabot version
updates work independently of Actions and are disabled through repository
settings, with no stable REST API. The owner disabled vulnerability alerts and
automated security fixes; version-update disablement still needs verification.

**Never merge Dependabot PRs into `main`.** Upstream supplies
`.github/dependabot.yml`; a pristine mirror retains it. Dependency changes enter
`main` only by fast-forwarding upstream history. The owner's 2026-09-08 reset
removed the 12 earlier Dependabot commits from `origin/main`. They were never
ancestors of `liquid`. The script's ancestry gate detects recurrence without
changing Actions or Dependabot settings.

## Regression proof

Run on macOS without containers, with at most six build jobs:

```sh
export GOMAXPROCS=6 GOFLAGS=-p=6
go build ./... && go vet ./...
shellcheck sync.sh hack/sync-upstream.sh
python3 hack/test-sync-upstream.py
```

The regression suite creates and removes scratch repositories under this
worktree. It exercises the real script against local upstream fixtures through
Git URL rewriting; the configured upstream URL and disabled push URL remain
subject to the production guards. Fixtures prove a clean dry run, a successful
fast-forward/rebase, an unchanged second run, mirror divergence from a deliberate
commit on `main`, explicit conflict paths (including with rerere enabled), and
licence, inconsistent guide records, unsafe merge drivers, and baseline failures.
No test pushes to any remote. The live upstream
dry run uses a separate scratch clone of the current refs; a real catch-up
of the shared branches is intentionally deferred.
