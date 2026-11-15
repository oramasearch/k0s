<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Orama's fork of k0s

This is Orama's fork of [`k0sproject/k0s`](https://github.com/k0sproject/k0s),
kept in sync with upstream and modified to support **liquid drips**
(see [liquid](https://github.com/oramasearch/liquid)): the k0s distribution that Orama Lake
installs on. [`docs/liquid/README.md`](docs/liquid/README.md) indexes what is changed.

[`sync.sh`](sync.sh) is the root entry point for maintaining the branch relationship below.
Our branches are never pushed to `upstream`: the `upstream` push URL is deliberately set to an
invalid value as a guard. Fixes that belong upstream are offered there as separate pull requests.

## Branch model

| Branch | Purpose | Rule |
|---|---|---|
| `main` | Pristine mirror of `upstream/main` | **Only ever fast-forwarded from upstream.** Never commit here. |
| `liquid` | Our changes | Rebased onto `main` after each sync. |

Keeping `main` pristine means `git diff main..liquid` is always exactly our delta —
which is what makes upstream syncs reviewable rather than archaeological.

**Last synced upstream tag:** `v1.37.0-alpha.1+k0s.0`

**Last synced upstream commit:** `444794a05883e34dcf796f695ac4d00220556fa3`

This is the verified upstream base already included in `liquid`, not a claim that
the current upstream tip has been integrated; catching up is a separate rebase.

## Syncing with upstream

```sh
./sync.sh --dry-run
./sync.sh
```

Use a clean, dedicated clone on `liquid` for an actual sync. The script validates
the guarded `upstream` remote, fast-forwards `main`, rebases `liquid`, checks
licence files against upstream, and records the tag and commit in this file and
the guide together. It never pushes. The dry run fetches and performs preflight
checks without changing branches or files. An unchanged second run creates no
commit. Run at every upstream release; review the dry run weekly between releases.
See [the upstream-sync guide](docs/liquid/upstream-sync.md) for options, publishing,
conflict recovery, and verification.

## Licence obligations

k0s is **Apache License 2.0** for code, with `docs/` under **CC-BY-SA-4.0**. GitHub's API
reports the licence as "Other" because of that dual arrangement; the code licence is
unambiguous.

The changes in this fork are contributed under upstream's terms, as any k0s contribution is:
Apache License 2.0 for code and CC-BY-SA-4.0 for `docs/`, with upstream's header convention
("SPDX-FileCopyrightText: k0s authors"). They are written to be offered upstream. Distributing modified binaries carries
three obligations:

1. Include the `LICENSE`.
2. Retain the `NOTICE` file and its attributions when the selected upstream ref includes one.
3. **State that we changed the files** — §4(b) requires prominent notices of modification.
   [`NOTICE`](NOTICE) states that k0s was modified and lists every upstream file
   it changed (regenerate the list after each sync). [`docs/liquid/README.md`](docs/liquid/README.md)
   is the durable index of our modifications. The branch difference helps review them, but neither a branch
   split nor an index proves that every changed file carries the required notice.

The current upstream tree has no root `NOTICE`. The sync script reports that
absence; when present, it requires the upstream `NOTICE` to survive byte-for-byte.
It also requires unchanged `LICENSE` and `docs/LICENSE` files and a recorded
modification declaration and index. Review modified-file notices and packaged
attributions before distributing; the script checks repository contents, not
binary or documentation packages.

The `docs/` directory is **share-alike**. If we modify and distribute k0s documentation,
those modifications must be licensed CC-BY-SA-4.0. Prefer keeping liquid documentation in
the liquid repository rather than editing `docs/` here.

## What "support drips" means

The design lives in [liquid](https://github.com/oramasearch/liquid), not here. The integration points are the deployment
runtime and the chunk store:

- the node fetching image content as **chunks** it does not already have, across images
  rather than within one, implemented by the embedded liquid node store and containerd proxy
  snapshotter documented in `docs/liquid/chunk-aware-node.md`;
- immutable bootc node images selected by digest, with signed machine configuration,
  controller-first image swaps, automatic rollback, and certificate-only audited
  break-glass access documented in `docs/liquid/node-image.md` and
  `docs/liquid/break-glass.md`;
- deploy-on-drip, driven by the drip fan-out rather than by a CI trigger;
- attestation verification at admission, so what runs is what was attested;
- microVM isolation for every customer workload on shared workers, with namespaces
  reserved for environments belonging to one trusting customer;
- declared Cilium, Gateway API, local-path, and Longhorn components whose versions and
  external replacement paths remain under cluster-configuration control.
- a default Cilium Gateway, cert-manager ACME issuer template, Vector-to-Loki log path,
  and environment admission policy for the Lake platform actions.
- lifecycle runbooks and SSH automation for upgrades, recovery, node replacement,
  certificate rotation, and metrics for those declared components.

Admission verification is a k0s static-Pod and webhook integration around the embedded
liquid verifier; its policy and operations are documented in `docs/liquid/admission.md`.

See `docs/features/13-deployment-runtime.md` and `docs/features/22-change-capture.md` in
the liquid repository.

## Dependabot and the mirror

The `main` mirror takes commits from upstream only, so no automated pull request is merged into
it. If `main` cannot fast-forward, the sync script stops and names the likely cause. It never
merges around mirror drift, resets `main`, or changes repository settings.
