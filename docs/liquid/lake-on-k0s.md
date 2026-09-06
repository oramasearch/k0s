<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Lake on one k0s node

This runbook installs the `Lake/hello` sample on a fresh Linux host running one liquid k0s
controller-worker. The installer builds k0s from the current checkout unless a liquid k0s artifact is
provided, waits for the declared liquid components, installs CloudNativePG and orama-operator, applies
the single-node Lake sample, and proves HTTP access through a Cilium Gateway API `NodePort`.

The live cluster run remains deferred to a Linux host. k0s cannot run on the macOS development host,
and this project does not use Docker or a VM there (decision 33).

## Requirements

Use a dedicated Linux host with a supported init system and no existing `/var/lib/k0s`. Install Git,
curl, and Helm 3; a source build also needs GNU Make and Go. Check out the `liquid` k0s branch and a
compatible orama-operator revision on that host. The operator chart must contain the Lake CRD and
controller.

Set these variables before invoking the installer:

```sh
export OPERATOR_CHART=/absolute/path/to/orama-operator/chart
export ORAMA_DOCKERHUB_USERNAME=orama-reader
export ORAMA_DOCKERHUB_TOKEN='read-only-token'
```

The token needs pull-only access to `oramasearch/orama-operator:v1-sandbox` and
`oramasearch/lake:v1-sandbox`. The installer tests both manifests anonymously first. If both images
are public, it ignores the two credential variables and creates no pull Secret. The anonymous
manifest checks returned HTTP 401 for both images on 2026-09-06, so credentials are currently
required.

`K0S_BINARY=/absolute/path/to/k0s` selects a published liquid artifact instead of building. Without
it, the installer runs:

```sh
make DOCKER="" EMBEDDED_BINS_BUILDMODE=none BUILD_GO_LDFLAGS_EXTRA="" build
```

The artifact must come from the liquid branch; an upstream k0s binary does not declare the Cilium,
Gateway API, or local-path components used here.

## Install and prove HTTP

From the k0s checkout, preserve the configuration and credential variables across `sudo` and run one
command:

```sh
sudo --preserve-env=OPERATOR_CHART,ORAMA_DOCKERHUB_USERNAME,ORAMA_DOCKERHUB_TOKEN,K0S_BINARY,NODE_ADDRESS,CNPG_CHART_VERSION,WAIT_TIMEOUT \
  hack/lake-on-k0s/install.sh
```

Omit `K0S_BINARY` from `--preserve-env` when building from source. The installer creates the pull
Secret as `orama-system/oramasearch-dockerhub` for the operator and as
`lake-dev/oramasearch-dockerhub` for Lake. It never prints the credential.

The successful final output includes the liquid component table, the selected endpoint, and Lake's
root response:

```text
Lake answered at http://127.0.0.1:<node-port>/
<response body>
```

Cilium exposes the route on every eligible node address. Set `NODE_ADDRESS` before running the
installer if the proof request should use a specific address instead of loopback. Open only the
reported node port in the host firewall when clients on another machine need access. No DNS record is
created.

The local sample is derived from orama-operator's
`config/samples/orama_v1alpha1_lake.yaml`. It keeps `Lake/hello` and the published sample images, but
sets one replica and changes both stores to the distribution's default `local-path` class. The shared
Lake store uses `ReadWriteOnce`, which is appropriate only for this one-replica proof.

## Differences from AKS

| Concern | AKS path | This single-node path |
|---|---|---|
| HTTP routing | ingress-nginx reconciles the operator's `Ingress`. | The distribution's Cilium controller reconciles `HTTPRoute/lake-hello` through a dedicated Gateway API class. The Lake's nginx ingress option stays off. |
| Storage | A cloud CSI class supplies durable volumes and may supply `ReadWriteMany`. | The default `local-path` class supplies node-local `ReadWriteOnce` volumes. Losing the node loses this proof's storage; use distributed or external storage for production. |
| External address | A cloud `LoadBalancer` assigns a public address. | `CiliumGatewayClassConfig/lake-on-k0s` requests a `NodePort`; no cloud load balancer is installed or awaited. |
| DNS | Cloudflare can point a hostname at the ingress load balancer. | The proof calls the node address and allocated port directly. It does not create a Cloudflare record. |

CloudNativePG is installed because Lake enables its control-plane Postgres by default. The installer
pins chart version `0.29.0`; set `CNPG_CHART_VERSION` only when validating a deliberate version change.

## Inspect the installation

```sh
sudo k0s liquid components
sudo k0s kubectl get lake hello
sudo k0s kubectl -n lake-dev get pods,pvc,service
sudo k0s kubectl -n liquid-system get gateway,httproute,service
```

## Teardown

The default teardown deletes the named Lake and route, uninstalls both Helm releases, deletes their
three namespaces, resets k0s, and removes `/usr/local/bin/k0s`. Run it only on the dedicated host used
for this proof:

```sh
sudo hack/lake-on-k0s/teardown.sh
```

Reboot after reset to clear the network rules that k0s documents as remaining on the host. To remove
only this workload and leave k0s running:

```sh
sudo env KEEP_K0S=1 hack/lake-on-k0s/teardown.sh
```

## Native checks

The macOS-safe checks are:

```sh
shellcheck hack/lake-on-k0s/*.sh
helm template orama-operator /path/to/orama-operator/chart \
  --namespace orama-system \
  --values hack/lake-on-k0s/operator-values.yaml
```

## Run record

Single-node run deferred to a Linux host (no Docker or VM on this machine; decision 33).

- Host and kernel:
- k0s commit or artifact digest:
- orama-operator commit and image digest:
- Lake image digest:
- Allocated node port:
- HTTP response:
- Operator and Lake readiness output:
