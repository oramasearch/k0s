# Upstream sync

`oramasearch/k0s` is a private copy of `k0sproject/k0s`. Its `main` branch is a
pristine upstream mirror, while `liquid` contains the private-copy changes. The
[`hack/sync-upstream.sh`](../../hack/sync-upstream.sh) script makes that relationship
repeatable and records the most recently synchronized upstream release tag in
[`ORAMA-FORK.md`](../../ORAMA-FORK.md).

## Why

Keeping `main` fast-forward-only means the difference from `main` to `liquid` remains
the complete private-copy delta. Rebasing that delta after each upstream release keeps
conflicts limited to the private changes and makes their review straightforward.

## Run and verify

Run the script from any directory inside a clean checkout:

```sh
hack/sync-upstream.sh --dry-run
```

The dry run queries the configured upstream without changing local branches or pushing.
It prints the current upstream tag, the target commit, and the fast-forward and rebase
that a real run would perform.

For an actual synchronization, first run the dry run, then run:

```sh
hack/sync-upstream.sh
```

The script fetches upstream tags and branches, checks that `main` can fast-forward,
rebases `liquid`, validates the Apache requirements, and commits the updated
last-synced tag in `ORAMA-FORK.md`. It does not push by default. After review, push the
result with:

```sh
hack/sync-upstream.sh --push
```

`--push` pushes `main` without force and pushes `liquid` with `--force-with-lease`.

To mirror a particular upstream release instead of the current upstream `main`, use:

```sh
hack/sync-upstream.sh --tag vX.Y.Z+k0s.N
```

The same flag can be used with `--dry-run` and `--push`.

If a rebase conflicts, the script leaves the `liquid` checkout in place and prints the
exact `git status`, resolution, staging, rebase-continuation, and cleanup commands.

## Licence checks

Before changing `main`, the script verifies that `LICENSE` exists at the root of the
selected upstream ref. After the rebase, it verifies that `liquid` retains `LICENSE`
and verifies that `ORAMA-FORK.md` states that this private copy is modified, satisfying
the Apache-2.0 modification-notice requirement.

Upstream currently provides no root `NOTICE`. When a selected upstream ref does include
one, the script reports that fact and verifies that `liquid` retains it; otherwise it
reports that no NOTICE check applies. No NOTICE is created by this repository.

## Change the procedure

Keep the expected upstream fetch URL and disabled push URL in
`hack/sync-upstream.sh` aligned with the repository remote configuration. Do not loosen
the checks: the script must reject a missing remote, a different fetch URL, or a real
upstream push URL. Any change to this procedure must update this guide and
`ORAMA-FORK.md` in the same commit.

## Cadence

Run the synchronization at every upstream k0s release. Record the resulting release
tag through the script so `ORAMA-FORK.md` remains the source of the last-sync status.
