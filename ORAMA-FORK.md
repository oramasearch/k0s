# Orama private copy of k0s

This is a **private copy** of [`k0sproject/k0s`](https://github.com/k0sproject/k0s),
kept in sync with upstream and modified to support **liquid drips**
(see [`oramasearch/liquid`](https://github.com/oramasearch/liquid)).

This file is the only addition at the repository root and is named so it will never
collide with an upstream path.

## Why a copy and not a fork

`gh repo fork` of a public upstream creates a **public** repository that **cannot be
flipped to private**, which would publish org and product context. Org policy forbids it.
This repository was created with `gh repo create oramasearch/k0s --private` and the
upstream history pushed into it.

**Never push this repository's branches to `upstream`.** The `upstream` push URL is
deliberately set to an invalid value as a guard.

## Branch model

| Branch | Purpose | Rule |
|---|---|---|
| `main` | Pristine mirror of `upstream/main` | **Only ever fast-forwarded from upstream.** Never commit here. |
| `liquid` | Our changes | Rebased onto `main` after each sync. |

Keeping `main` pristine means `git diff main..liquid` is always exactly our delta —
which is what makes upstream syncs reviewable rather than archaeological.

## Syncing with upstream

```sh
git fetch upstream --tags
git checkout main
git merge --ff-only upstream/main     # fails loudly if main was ever committed to
git push origin main --tags

git checkout liquid
git rebase main                        # resolve conflicts in OUR commits only
git push --force-with-lease origin liquid
```

Run this on a cadence. The longer the gap, the worse the rebase — k0s tracks Kubernetes
releases and moves quickly.

## Licence obligations

k0s is **Apache License 2.0** for code, with `docs/` under **CC-BY-SA-4.0**. GitHub's API
reports the licence as "Other" because of that dual arrangement; the code licence is
unambiguous.

Apache 2.0 permits private modification with no obligation to publish. **If we distribute
modified binaries**, three obligations attach:

1. Include the `LICENSE`.
2. Retain the `NOTICE` file and its attributions.
3. **State that we changed the files** — §4(b) requires prominent notices of modification.
   Our changes must be identifiable, which the `main`/`liquid` branch split already makes
   true.

The `docs/` directory is **share-alike**. If we modify and distribute k0s documentation,
those modifications must be licensed CC-BY-SA-4.0. Prefer keeping liquid documentation in
the liquid repository rather than editing `docs/` here.

## What "support drips" means

Design lives in `oramasearch/liquid`, not here. The integration points are the deployment
runtime and the chunk store:

- the node fetching image content as **chunks** it does not already have, across images
  rather than within one;
- deploy-on-drip, driven by the drip fan-out rather than by a CI trigger;
- attestation verification at admission, so what runs is what was attested.

See `docs/features/13-deployment-runtime.md` and `docs/features/22-change-capture.md` in
the liquid repository.
