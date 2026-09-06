<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Liquid-specific k0s changes

These pages document the private `liquid` branch additions. Upstream k0s documentation
remains unchanged unless an upstream behavior itself changes.

- [Chunk-aware node images](chunk-aware-node.md) describes the embedded node store, OCI
  annotation contract, containerd proxy snapshotter, cross-image chunk reuse, and fallback path.
- [Declared components](components.md) covers pinned versions, upgrades, and external
  replacements for the opinionated runtime defaults.
- [Multi-tenancy](multi-tenancy.md) records the customer isolation model and the
  namespace and microVM runtime declarations.
- [Lifecycle runbooks](lifecycle/README.md) cover upgrades, backup and restore, node
  replacement, certificate rotation, and observability for those defaults.
- [Upstream sync](upstream-sync.md) covers maintaining the private copy.
