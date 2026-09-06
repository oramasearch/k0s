<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Chunk-aware node images

k0s workers can run stock OCI images and liquid-projected images through one containerd CRI
configuration. The feature is enabled for newly created liquid configurations at
`spec.liquid.nodeStore.enabled`. Configurations created before the liquid fields existed do not
acquire the feature during an upgrade.

## Image paths

Stock OCI images follow containerd's normal path. The CRI image service resolves the manifest from
the configured registry, downloads its layers, and asks the `liquid` proxy snapshotter to prepare
them. Because the manifest has no liquid annotation, the proxy sends every operation to
containerd's `overlayfs` snapshotter without changing its mounts.

A liquid-projected image is still resolved through the registry. Its selected manifest carries
`io.liquid.tree=<digest>`. The manifest's config descriptor carries
`io.liquid.store=<sync-url>`; the proxy also accepts the value as an OCI config label. The proxy
uses the manifest digest supplied by containerd's inherited snapshot labels to read this contract
from containerd's local content store. No registry-specific API is part of the contract.

For a liquid image, the proxy asks the local node store to mount the tree, records the image's
containerd chain snapshots without unpacking the layer bodies, and returns the mounted liquid tree
as the read-only image root. Container snapshots retain an `overlayfs` upper directory and work
directory managed by containerd. The liquid tree is only a read-only lower directory.

OCI projection is owned by liquid issue 28, and serving projected manifests from a registry is
owned by lake issue 3. Until those land, tests construct the same annotations and serve them with a
registry double. Neither later change alters the node contract.

## One store per node

When enabled, k0s starts these worker components before containerd:

```text
liquid node-store serve \
  --root /var/lib/k0s/liquid \
  --listen unix:///run/k0s/liquid.sock \
  --control-listen 127.0.0.1:8082
```

The embedded liquid source is pinned to commit
`ba12a564c997208da5ace034b4d905e416525a42`, the merge commit that added the runtime node store.
The source is a submodule under `embedded-bins/liquid/src`, and the resulting Linux executable is
included in the k0s payload. A worker image must provide `/dev/fuse` and libfuse3 as described by
liquid and by k0s issue 16.

The node store owns one content-addressed store for every image on the node. A mount first receives
tree and blob metadata from the image's sync URL. Reads through liquid's VFS hydrate verified
content chunks on demand through `remote_blob_source::read_range`. Chunks are keyed by digest, not
by image, so a chunk fetched for one image is immediately local for every later image that uses it.
This is what “transfers only the chunks the node lacks, across images” means mechanically.

The control listener exposes `/healthz`, `/readyz`, `/metrics`, `/v1`,
`/v1/openapi.json`, and `POST /mcp`. Its metrics include fetched chunks, fetched bytes, and avoided
bytes per tree. The Unix listener is local to the worker and exposes the mount, unmount, prefetch,
and status calls used by the proxy.

## containerd registration

k0s writes `/etc/k0s/containerd.d/liquid.toml` only when the node store is enabled and the main
containerd configuration remains k0s-managed. The drop-in registers
`/run/k0s/liquid-snapshotter.sock` under `[proxy_plugins.liquid]` and selects `liquid` as the CRI
image snapshotter. Disabling the feature removes a k0s-managed liquid drop-in but preserves a file
at the same path if it is operator-managed.

The proxy delegates snapshot metadata and writable storage to containerd's `overlayfs` snapshotter.
Liquid mount identity is retained as inherited snapshot labels, which lets containerd garbage
collection and proxy restarts recover the correct tree and store URL.

## Failure behavior

If the liquid store cannot be reached and the image includes ordinary layers, the proxy returns to
the normal layer download and `overlayfs` path. The pod can still start, but it receives none of the
chunk-transfer benefit for that pull.

If the image has no usable layer path, the pull fails with an unavailable error that names the tree,
store URL, and node-store failure. Kubernetes records that pull failure on the pod. Invalid or
conflicting liquid snapshot metadata fails rather than selecting a different tree. Digest failures
inside the node store are never published as local chunks.

## Verification

Unit tests cover containerd drop-in rendering, managed-file removal, stock dispatch, liquid
dispatch, writable overlay mounts, layer fallback, and unavailable-store errors. The
`check-liquid-nodestore` integration target covers two trees that differ by one file, the metric
delta across their pulls, a stock OCI image, and successful layer fallback when the liquid store
is unreachable. The no-layer unavailable error is covered at the snapshotter boundary because an
OCI manifest with no layers gives containerd no extraction snapshot on which to pass the liquid
annotations.
