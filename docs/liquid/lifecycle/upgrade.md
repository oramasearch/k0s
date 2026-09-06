<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Upgrade

## What

`hack/lifecycle/upgrade.sh <version>` writes an Autopilot `Plan`, applies it
from the selected controller, and waits for `Completed`. It discovers the
controller and worker targets from the lifecycle inventory. Autopilot updates
controllers one at a time with readiness and quorum checks, then updates
workers at the configured concurrency (one by default). While a controller is
being updated, the script reads plan status from another configured controller
when one is available.

For a released binary, the script derives the k0s release URL from `<version>`.
Set `K0S_LIFECYCLE_BINARY_URL` for a private build, or set
`K0S_LIFECYCLE_BINARY_URL_TEMPLATE` with `{version}` and `{arch}` placeholders.
Set `K0S_LIFECYCLE_FORCE_UPDATE=true` only when a rebuilt binary deliberately
retains the installed version string.
The platform list starts empty and accepts the first controller or worker
architecture when the script runs with `set -u`.

## Why

The k0s binary and liquid components have separate version lifecycles. The
stored `spec.liquid.components` declarations select Cilium, Gateway API,
local-path, and distributed storage versions. A binary upgrade must not change
those declarations or their Helm `Chart` resources.

## How to verify

Run these commands before and after the upgrade:

```shell
hack/lifecycle/check-health.sh
ssh controller0 /usr/local/bin/k0s liquid components
```

The second command must show the same declared and deployed component versions
on both sides. Inspect a plan while it is running with:

```shell
ssh controller0 /usr/local/bin/k0s kc get plan autopilot -o yaml
```

## How to change

Run the rollback as another plan to the previously retained binary, using its
previous version and URL:

```shell
K0S_LIFECYCLE_BINARY_URL=https://artifacts.example.net/k0s-previous \
  hack/lifecycle/upgrade.sh vX.Y.Z+k0s.N
```

Autopilot plans are immutable. The script removes only a completed `autopilot`
plan and refuses to replace an active plan. Change a liquid component in a
separate operation as described in [Declared components](../components.md),
after checking that its own compatibility policy permits the change.
The generated plan is a temporary local file and is removed on exit, including
when the shell runs with `set -u`.
