<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Building without the GPL embedded binaries

Two of the executables that k0s embeds by default are licensed under the
GPL-2.0: keepalived (control plane load balancing) and iptables
(`xtables-legacy-multi`, `xtables-nft-multi`). Every other embedded executable
is under a permissive licence. The `EMBEDDED_BINS_EXCLUDE` build variable
leaves executables out of the embedded bundle:

```sh
make EMBEDDED_BINS_EXCLUDE='keepalived xtables-legacy-multi xtables-nft-multi'
```

The variable is defined in `embedded-bins/Makefile.variables`. The bundle is
built from the resulting list only, so binaries already staged in
`embedded-bins/staging/` by an earlier build are not picked up, and changing the
variable rebuilds the bundle.

## Behaviour of such a build

k0s looks for an executable that is not embedded in its bin directory and then
in the `PATH` (`pkg/assets.StageExecutable`, unchanged upstream behaviour).

- **keepalived.** Control plane load balancing needs keepalived installed on
  every controller. Without it, a controller with
  `spec.network.controlPlaneLoadBalancing` enabled fails to start with
  `control plane load balancing requires keepalived, which is neither embedded
  in k0s nor installed on this host`. Clusters without CPLB are unaffected.
- **iptables.** k0s links the `iptables`, `ip6tables` and their `-save` and
  `-restore` commands to the host's `xtables-*-multi` executable for the
  selected mode. With only one mode installed, that mode is used. With none,
  k0s links nothing, removes the links an earlier run made, and the host's
  `iptables` from the `PATH`, if any, is used. An explicit `--iptables-mode`
  whose executable is missing is an error. kube-proxy and kube-router run as
  Pods with their own iptables, and kube-proxy's `nftables` mode needs none.

The upstream pages changed with it are
[External runtime dependencies](../external-runtime-deps.md#builds-without-some-embedded-binaries)
and [Control plane load balancing](../cplb.md).
