<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Immutable node images

Liquid k0s nodes use a bootc image identified by a complete OCI pull reference and
SHA-256 digest. The digest, rather than a mutable tag, is the deployment identity.
The image contains the k0s binary and all of its embedded binaries, the k0s systemd
unit, the node-image agent, and an OpenSSH server that is disabled by default.

bootc was selected over Talos itself and a custom squashfs A/B updater. Talos would
replace k0s, while a custom updater would make this project responsible for image
switching, boot selection, and rollback. bootc already supplies those operations and
is the format targeted by liquid issue #78. Until that projection exists,
[`hack/node-image/Containerfile`](../../hack/node-image/Containerfile) is the declared
recipe. Replacing the recipe with projected output does not change the node API or
upgrade procedure.

## Host contract

`/etc/k0s` and `/var/lib/k0s` are the only persistent writable application state.
`/run/k0s` holds transient access state and is cleared at boot. The operating system
and k0s executable live in the read-only bootc deployment. Package-manager executables
are removed from the image. Operators do not install packages or edit configuration on
a running host.

First boot applies `/etc/k0s/node-config-input.yaml` with `k0s node-config`. The
command verifies the detached Ed25519 signature against the public key baked into the
image at `/usr/share/k0s/node-config-signing.pub`, validates the schema, then atomically
writes the applied configuration, service environment, and customer OpenSSH CA. An
invalid signature, unknown field, non-Ed25519 CA, relative token reference, excessive
break-glass lifetime, or non-HTTPS lake audit endpoint prevents k0s from starting.

The schema is [`hack/node-image/node-config.schema.json`](../../hack/node-image/node-config.schema.json).
The signed payload is the compact JSON representation of `apiVersion`, `kind`, and
`spec`, in that order; `signature` is excluded. A machine configuration has this form:

```yaml
apiVersion: liquid.k0sproject.io/v1alpha1
kind: NodeConfig
spec:
  role: worker
  joinTokenRef: /etc/k0s/join-token
  trustedUserCA: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... customer-ssh-ca
  breakGlass:
    enabled: true
    maxTTLSeconds: 3600
    auditEndpoint: https://lake.example/v1/audit/events
signature:
  keyID: production-node-config-2026
  value: BASE64_ED25519_SIGNATURE
```

The join token and optional lake mTLS files are delivered as separate machine secrets;
the signed configuration refers to their absolute paths and never embeds them. The
node agent uses `/var/lib/k0s/kubelet.conf` on workers and the local admin kubeconfig
on controllers. Before issuing an instruction, the controller binds the exact
`system:node:<name>` identity to `get`, `patch`, and `update` only the Node resource
with that name. The agent cannot list or modify another Node. Deployments that use a
separate restricted agent kubeconfig pass it with the node-agent unit's `--kubeconfig`
flag.

## Building

Use a bootc-capable image builder such as Podman. The build script copies only the
given k0s executable, the pinned node-config public key, and the declared recipe into a
temporary context:

```sh
hack/node-image/build.sh ./k0s registry.example/liquid/k0s-node:build-42 ./node-config-signing.pub
```

It prints `registry.example/liquid/k0s-node:build-42@sha256:...`. Push the image under
that digest before creating a plan. Do not use the mutable tag in a `NodeImage`.

## Image swap and rollback

Apply a cluster-scoped `NodeImage`:

```yaml
apiVersion: liquid.k0sproject.io/v1alpha1
kind: NodeImage
metadata:
  name: node-build-42
spec:
  digest: registry.example/liquid/k0s-node@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  selector:
    matchLabels:
      liquid.orama.dev/node-image: managed
  strategy:
    deadline: 10m
    automaticRollback: true
```

The controller resolves the selector once. The resulting status target list is
immutable. Targets carrying a control-plane role label are sorted before workers, and
only one target is active. For each target the controller asks its node agent to run
`bootc switch` with the digest-pinned reference and reboot. A boot-ID marker prevents
the agent from acknowledging the switch before a real reboot. The controller advances
only after the node is Ready and reports the requested digest.

If the node is not Ready before `strategy.deadline`, the controller requests `bootc
rollback` and another reboot. A Ready node on the previous deployment is recorded as
`RolledBack`, and the plan becomes `Failed`; later targets are not touched. Disabling
automatic rollback leaves the failed deployment selected and should be reserved for a
supervised recovery.

Changing a plan's digest or generation after reconciliation starts fails the plan.
Create a new `NodeImage` for another deployment. Inspect all plans or one plan with:

```sh
k0s liquid node-image status
k0s liquid node-image status node-build-42
```

The optional name limits the table to one plan.

## Test harness

[`hack/node-image/test/run.sh`](../../hack/node-image/test/run.sh) is the QEMU entry
point. It requires two bootc qcow2 fixtures, digest-pinned old and new images in a
registry reachable by the guests, signed node configurations, and the lake capability
double described beside the script. The test joins a controller-worker and worker,
updates the first target, makes the second target miss its readiness deadline, and
checks the automatic rollback. It then exercises the certificate-only break-glass
path and verifies that the audit stream contains open, login, session-close, and
window-close records.

The upstream `hack/ostests` harness provisions cloud hosts and cannot boot a local OS
image, so it is not used for this test.

## Projection handoff

Liquid issue #78 replaces the `Containerfile` build input with an ostree/bootc commit.
The output must retain the systemd units, signed machine-config contract, k0s binary,
node agent, disabled SSH default, and digest identity described here. No `NodeImage` or
`BreakGlass` API change is expected.
