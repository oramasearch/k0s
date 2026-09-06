<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Node replacement

## What

`hack/lifecycle/replace-node.sh <node>` uses the inventory role for a safe
replacement. A worker is drained, removed from Kubernetes, reset, joined with a
short-lived pre-shared worker token, and waited until Ready. A controller is
removed from Autopilot and etcd, reset, supplied with the current cluster
configuration and a short-lived controller token, then joined and waited until
its etcd member reports `Joined=True`.

Controller replacement requires at least three configured controllers. The
script operates from another controller, so it can replace the controller named
by `K0S_LIFECYCLE_CONTROLLER` as well.

## Why

Drain prevents ordinary workloads from being terminated without a scheduling
opportunity. Explicitly leaving etcd avoids a stale member consuming quorum.
Longhorn detects the lost node and schedules replicas to eligible storage nodes;
the script waits until its existing volumes report healthy. Local-path volumes
cannot move automatically, so their application data requires the backup path
in [Backup and restore](backup-restore.md).

## How to verify

Run a health check, replace one node, and run the check again:

```shell
hack/lifecycle/check-health.sh
hack/lifecycle/replace-node.sh worker2
hack/lifecycle/replace-node.sh controller2
hack/lifecycle/check-health.sh
```

For a distributed storage installation, also inspect the volume replicas and
application data after the replacement:

```shell
ssh controller0 /usr/local/bin/k0s kc -n longhorn-system get volumes.longhorn.io
```

## How to change

Use a new SSH target for a newly provisioned machine by changing only that
node's third inventory column. The replacement node needs k0s installed at
`K0S_LIFECYCLE_K0S` and the storage-node prerequisites already present. Set
`K0S_LIFECYCLE_STORAGE_RESCHEDULE_COMMAND` when an external storage provider
needs an additional replica-rebuild action after a node replacement. The
script stops a detected OpenRC `k0sworker` or `k0scontroller` service before
using that configured k0s path for the stop and reset operations. This prevents
the service supervisor from restarting k0s during replacement on bootloose
nodes while leaving systemd and direct k0s installations on their usual path.
The OpenRC test is run by `sh -ceu` on the remote host so its conditional
command is parsed there. The replacement installation is followed by a
service start retried through the detected init script after k0s records
the new service definition. This lets OpenRC register the replacement service
before the script treats it as ready; systems without that script use `k0s
start`. Controller configuration is staged in a temporary local file and its
cleanup remains safe when the script runs with `set -u`.
