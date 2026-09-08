<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Liquid-specific k0s changes

These pages document the private `liquid` branch additions. Upstream k0s documentation
remains unchanged unless an upstream behavior itself changes.

- [Chunk-aware node images](chunk-aware-node.md) describes the embedded node store, OCI
  annotation contract, containerd proxy snapshotter, cross-image chunk reuse, and fallback path.
- [Immutable node images](node-image.md) describes the bootc host contract, signed
  machine configuration, image-swap rollout, health gate, and automatic rollback.
- [Break-glass node access](break-glass.md) gives the certificate-only, time-bounded
  procedure and the lake audit records required for every access window.
- [Image admission](admission.md) describes the liquid verifier, namespaced policy routing,
  pinned transparency-log trust, static Pod deployment, certificate handling, and fail-closed
  behavior.
- [Declared components](components.md) covers pinned versions, upgrades, and external
  replacements for the opinionated runtime defaults.
- [Air-gapped inventory](airgap.md) covers exact component chart rendering, images,
  architecture selection and chart export to an internal repository.
- [Multi-tenancy](multi-tenancy.md) records the customer isolation model and the
  namespace and microVM runtime declarations.
- [Platform surface](platform-surface.md) defines deploy, logs, environment values,
  domains, the default Gateway and certificate path, tenant RBAC without Secret access,
  and the supporting sealed-secret admission policy.
- [Lake on one k0s node](lake-on-k0s.md) installs the operator and `Lake/hello`, adapts
  storage and external routing to the declared on-premises components, and records the Linux run.
- [Lifecycle runbooks](lifecycle/README.md) cover upgrades, backup and restore, node
  replacement, certificate rotation, and observability for those defaults.
- [Upstream sync](upstream-sync.md) covers maintaining the private copy.
