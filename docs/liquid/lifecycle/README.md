<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Liquid lifecycle runbooks

These runbooks operate the declared liquid components together with the k0s
control plane. They use `hack/lifecycle/` from an operator workstation and an
SSH inventory with one `role node ssh-target` row per node:

```text
controller controller0 root@controller0.example.net
controller controller1 root@controller1.example.net
controller controller2 root@controller2.example.net
worker worker0 root@worker0.example.net
worker worker1 root@worker1.example.net
worker worker2 root@worker2.example.net
```

Set `K0S_LIFECYCLE_INVENTORY` to that file. SSH targets can be host aliases in
`K0S_LIFECYCLE_SSH_CONFIG`, which is how the scripts run against bootloose and
how operators can select identities, ports, and bastions. `K0S_LIFECYCLE_K0S`
defaults to `/usr/local/bin/k0s`; set it when k0s is installed elsewhere.

Run [health checks](../../../hack/lifecycle/check-health.sh) before and after
any disruptive operation. The checks require all worker nodes to be Ready,
every configured controller to remain an etcd member, certificates to have at
least 30 days remaining, declared components to be deployed, and no pending
Autopilot plan.

`make check-liquid-lifecycle` builds the preceding `origin/liquid` revision in
an isolated cache with that revision's version and source date,
starts three controllers and three workers from that binary, and exercises the
five procedures through the SSH inventory. It verifies that declared component
versions survive the binary upgrade and that the HA etcd metrics job set is
`kube-scheduler`, `kube-controller-manager`, and `etcd`.

The test uses the same minimal shared HA configuration as `inttest/ap-ha3x3`,
adds the branch's liquid component set without serializing defaulted per-node
addresses, sets Cilium's required `network.provider: custom`, then enables
dynamic configuration and the metrics scraper on every controller.

- [Upgrade](upgrade.md)
- [Backup and restore](backup-restore.md)
- [Node replacement](node-replacement.md)
- [Certificate rotation](certificate-rotation.md)
- [Observability](observability.md)
