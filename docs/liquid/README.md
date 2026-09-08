<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Liquid-specific k0s changes

These pages document the private `liquid` branch additions. Upstream k0s documentation
remains unchanged unless an upstream behavior itself changes.

## Use the liquid distribution

Use the `liquid` branch and its `k0s` artifact for liquid clusters. `main` is the
pristine `upstream/main` mirror and is only fast-forwarded from upstream; liquid
changes are rebased onto it. An upstream k0s binary does not declare the liquid
components required by the Lake runbooks.

Liquid k0s has a Linux-only runtime. On a Mac, the native no-Docker command below
builds a Linux ARM64 ELF artifact; that cross-build is expected, but the artifact
cannot run on macOS:

```sh
make DOCKER="" EMBEDDED_BINS_BUILDMODE=none BUILD_GO_LDFLAGS_EXTRA="" build
```

Use the resulting `k0s` artifact on a Linux host, or provide a published artifact
from the `liquid` branch as `K0S_BINARY` to the connected Lake installer. The
connected runbook retains its explicit Linux-host validation deferral.

- [Lake on one k0s node](lake-on-k0s.md) installs Lake on a connected Linux host.
- [Air-gapped Lake installation (orama-operator repository)](https://github.com/oramasearch/orama-operator/blob/main/docs/install/k0s.md) gives the air-gapped Lake procedure. It lives with the operator because the transferred Lake and operator artifacts are managed there; its Linux-host validation remains deferred.

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
