<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Immutable-node QEMU test

`run.sh` boots two prepared bootc qcow2 disks. The controller fixture exposes the
Kubernetes API on guest port 6443, and both fixtures contain signed machine
configurations whose customer SSH CA is the CA used by the lake test double.

Required environment:

- `CONTROLLER_DISK` and `WORKER_DISK`: writable qcow2 fixture paths;
- `QEMU_SYSTEM`: the appropriate `qemu-system-*` executable;
- `QEMU_MACHINE_ARGS`: architecture-specific machine, CPU, and firmware arguments;
- `TEST_KUBECONFIG`: host kubeconfig for the forwarded controller API;
- `NEW_IMAGE`: digest-pinned bootc image reachable from both guests;
- `LAKE_ENDPOINT`: HTTPS endpoint for a test double implementing
  `issue_ssh_user_certificate` and audit-event writes;
- `SSH_KEY`: private key corresponding to the public key certified by that double.

The test double returns JSON containing `certificate` from
`POST /v1/pki/issue_ssh_user_certificate`, accepts the four audit event types at the
machine configuration's audit endpoint, and returns them from `GET /v1/audit/events`.
The script checks controller-first ordering, pauses the worker until its deadline,
observes rollback after resuming it, opens a one-minute access window, logs in with
the issued certificate, and verifies that SSH is unavailable after expiry.

Run `./run.sh --check` to validate the harness and environment without starting VMs.
Successful full runs write a transcript and VM serial logs beneath a fresh temporary
directory and print its path.
