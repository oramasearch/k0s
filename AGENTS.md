<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# k0s repository instructions

This is a fork of [`k0sproject/k0s`](https://github.com/k0sproject/k0s) carrying the changes for
liquid. Read [ORAMA-FORK.md](ORAMA-FORK.md) before changing it: it defines the branch model, the
upstream-sync procedure (`sync.sh`), and the licence obligations.

- `main` is a pristine upstream mirror; do not commit to it. Work lands on `liquid`, and pull
  requests target `liquid`.
- Build with `make build`, run the unit tests with `make check-unit`, and lint with `make lint`.
- Every modified upstream file must keep a notice of modification, and every change is indexed in
  [`docs/liquid/README.md`](docs/liquid/README.md) (Apache-2.0 §4(b)).
- Document every change in the matching `docs/liquid/` page in the same commit.
- Fixes that belong upstream are offered to upstream as separate pull requests, with no fork-specific
  context.
- Do not add AI attribution to commits, pull requests, or documentation.
- Do not include time estimates.
- Never write the word "seam"; name the actual mechanism (interface, extension point, boundary).
