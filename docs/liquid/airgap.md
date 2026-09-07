<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Air-gapped liquid inventory

Use the exact liquid k0s binary and the complete cluster configuration intended for
installation. `k0s airgap list-images --all --platform=linux/amd64` includes the base
k0s images and every enabled, managed declaration in `spec.liquid.components`.
It renders the declared chart version with `LiquidComponentValues`, the deployment's
release name and namespace, and the binary's Kubernetes version. Chart versions
are not application image tags: local-path chart `0.0.38`, for example, runs
`docker.io/rancher/local-path-provisioner:v0.0.37`.

Inventory generation runs on the connected staging host. It downloads the exact
chart archives from the declared HTTPS repositories and verifies their SHA-256
against the repository index. `--chart-dir=PATH` instead reads
`<chart-name>-<declared-version>.tgz` from a transferred directory, with no network
fallback. A missing archive, version mismatch, invalid image reference, invalid
values or render failure fails the command before it prints any image list.
`pkg/airgap.GetImageURIs` now returns `([]string, error)` so callers cannot mistake
a partial base list for a complete distribution inventory.

## What the image list covers

Both install and upgrade manifests are rendered, including Helm hooks and test
Pods. The inventory includes container and init-container images, image arguments
passed to controllers, image environment variables, and Pod templates stored in
ConfigMaps. It deduplicates references and retains the chart's tags and digests.

| Declared component | Images selected by the default chart and values |
|---|---|
| Cilium | Agent, operator and Envoy; agent init containers share the agent image. |
| Gateway API | No images; its chart supplies CRDs. It still requires a chart archive. |
| cert-manager | Controller, cainjector, webhook, ACME HTTP-01 solver and startup API-check hook. |
| Vector | Node log agent. Additional containers selected through values are included. |
| local-path | Provisioner and the BusyBox helper used when provisioning or deleting volumes. |
| Longhorn | Manager, UI, engine, instance manager, share manager, backing-image manager, support-bundle kit and six CSI images, including images created later by controllers. |

Longhorn CSI image repositories and tags must remain explicit. Empty values that
make the Longhorn controller choose compiled fallback images are rejected, since
those choices are absent from the rendered chart.

For a liquid configuration, `--all` includes optional base images such as the
push gateway and node-local load balancers. It does not add unused kube-router or
Calico alternatives and does not enable disabled or externally replaced liquid
components. A legacy configuration without `spec.liquid` retains the upstream
inventory behavior.

This is the distribution component inventory, not an inventory of arbitrary
workloads, `spec.extensions.helm` releases, external replacements, host bootc
images, or operator/Lake/CloudNativePG dependencies. Append those applications'
rendered and controller-selected images and transfer their charts separately.
The embedded liquid worker executable is part of the k0s binary, not a container
image.

## Selection and changes

- **Disabled components:** `enabled: false` excludes their images and charts, even
  with `--all`.
- **External replacements:** `replacement: external` excludes the component.
  Inventory and transfer the replacement's artifacts independently. Leaving an
  existing release installed does not make k0s its offline artifact owner.
- **Architecture:** managed components target Linux amd64 and arm64. The current
  charts use the same references for both; the OCI bundle command selects the
  requested platform's content. Build a bundle for each worker architecture and
  let missing registry platforms fail preparation. Linux arm and riscv64 are
  rejected while managed liquid components are enabled. Windows inventory contains
  only applicable base images; it does not imply support for Windows liquid workers.
- **Version, repository or value changes:** prepare fresh exact archives, rerender
  the list and regenerate the image bundle. Do not substitute the image tag for
  the chart version or reuse an earlier archive under a new filename. Include all
  packaged Helm dependencies; preparation rejects archives with missing dependencies.
- **Custom charts:** review images selected dynamically by their controllers and
  any cluster-dependent template logic. The renderer has the binary's Kubernetes
  capabilities, not a live cluster's discovery or secrets. Runtime image choices
  must be explicit in the rendered resources before using this inventory offline.

The default local-path helper uses `busybox:latest`. Preserve its resolved content
digest in the installation record and retain the chart's `IfNotPresent` pull
policy when importing a bundle. Changing that tag or any image pull policy changes
what must be validated with public egress denied.

## Chart transfer

An OCI image bundle does not contain Helm chart archives. Transfer all six default
component archives, including Gateway API's CRDs, and their packaged dependencies.
Serve them from an internal HTTPS Helm repository reachable by every controller
before k0s starts. Set every enabled, managed component's `chart.repository` to
that internal URL in the installation configuration. The extensions controller
uses those declared repositories for installation, reconciliation and upgrades.
Disabled and external declarations create no repository download requirement.

The internal repository, DNS and its trusted TLS certificate must be available
independently of the cluster being installed. Retain the original archives and
any included licences and notices. Record chart and image-bundle checksums with
the exact binary and configuration. Transfer the application's additional charts,
images and manifests separately.

## Verification

```sh
export GOMAXPROCS=6 GOFLAGS=-p=6
go build ./... && go vet ./...
go test ./pkg/airgap/...
```

The regression test checks the default component images. The chart comparison
check enumerates every `LiquidComponents` field, loads all declared archives,
compares independently read rendered image lines with the inventory and records
Gateway API as a chart with no images. Separate assertions cover controller-created
ancillary images and hook, init-container, value, version and architecture cases.
The tests fetch pinned archives once; set `K0S_AIRGAP_CHART_DIR` to an existing
archive directory to run them without public access.

Fresh-node readiness with public egress denied is still an owner-run Linux proof,
tracked by #13 and the acceptance list in #37. Record the binary/configuration,
archive and image-bundle digests, target architecture, egress-denial evidence and
`k0s liquid components` readiness for every managed component. Native inventory
and rendering checks do not establish node readiness.
