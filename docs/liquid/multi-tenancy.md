<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Multi-tenancy

## Question

What isolates customer workloads, and which cheaper mechanism is sufficient for
environments that belong to the same trusting customer?

## Decision

**Customer workloads use microVMs from the start.** On shared worker nodes every customer workload pod
sets `runtimeClassName: untrusted`, backed by Kata Containers or a Firecracker
configuration. No VM boundary contains workloads from more than one customer.

Namespaces remain the environment boundary inside one trusting customer. A
customer that needs an API-server and infrastructure boundary receives a separate
k0s cluster, as the on-premises lake installation already does. vcluster is not
adopted.

This is layered isolation. Pod Security Standards and network policy reduce the
workload's authority but do not turn a namespace into a security boundary. The
microVM supplies the guest-kernel boundary on shared workers. A separate cluster
supplies the control-plane, node, and failure-domain boundary.

## Options considered

| Model | Security boundary | Operating cost | Use |
|---|---|---|---|
| Namespace with Pod Security Standards `restricted`, default-deny `NetworkPolicy`, `ResourceQuota`, and `LimitRange` | Shares the host kernel and Kubernetes API server. It does not contain a hostile workload. | Lowest: ordinary namespaced resources and policy. | Environments and services owned by one trusting customer. |
| vcluster | Separates tenant API servers but workloads still share the host kernel. It does not contain a hostile workload on its own. | Moderate: one virtual control plane and another component to upgrade, observe, and recover per tenant. | Not adopted; it does not provide the kernel boundary required here. |
| Separate k0s cluster | Separates the API server, data store, workers, and cluster failure domain. | Higher fleet cost, although k0s has a small footprint and one cluster per customer matches lake's on-premises shape. | Customers requiring a full infrastructure boundary. Fleet lifecycle belongs to k0s #20. |
| Kata Containers or Firecracker through a `RuntimeClass` | Gives each workload pod a guest-kernel boundary while nodes and the Kubernetes control plane remain shared. | Requires hardware virtualisation, a compatible runtime on selected workers, more memory and startup overhead, and microVM-aware operations. | Required for customer workloads on shared workers. |

## Namespace baseline

The `tenant-profile` command renders the k0s-owned template from
`static/manifests/liquid/tenant-namespace/profile.yaml`. Apply it before creating
workloads:

```sh
tenant_namespace=preview-change-123
k0s liquid tenant-profile "${tenant_namespace}" | k0s kubectl apply -f -
```

The output contains a namespace labelled to enforce, audit, and warn at Pod
Security Standards `restricted`; a deny-all ingress and egress `NetworkPolicy`;
a `ResourceQuota`; a `LimitRange` that supplies default container requests
and limits; and a namespaced tenant Role and RoleBinding. The binding grants only
the workload permissions listed in the [platform access contract](platform-surface.md#tenant-rbac-is-authoritative)
to `liquid:tenant:<namespace>`, with no Secret access. It must be the tenant's only
grant in that namespace. Network access remains closed until the operator adds explicit policy
for required DNS and service paths. Quota values are baseline safeguards and may
be replaced by a customer-specific policy.

This profile is appropriate for preview environments and services of the same
customer. It is defence in depth around the microVM requirement, not an
alternative to it.

## `untrusted` runtime class

The branch declares an `untrusted` `RuntimeClass` with containerd handler `kata`.
On every worker selected for untrusted workloads, the cluster operator must:

1. Provide CPU hardware virtualisation and expose `/dev/kvm`. A worker that is
   itself virtualised also needs nested virtualisation from its infrastructure.
2. Install Kata Containers, including `containerd-shim-kata-v2`, and maintain its
   guest kernel and VM image.
3. Add a k0s-managed containerd drop-in at `/etc/k0s/containerd.d/kata.toml`:

   ```toml
   version = 3

   [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata]
     runtime_type = "io.containerd.kata.v2"
   ```

4. Verify that a Kata pod starts, then label the worker and apply the declaration:

   ```sh
   k0s kubectl label node worker-1 liquid.oramasearch.com/runtime=kata
   k0s liquid untrusted-runtime-class | k0s kubectl apply -f -
   ```

5. Set `runtimeClassName: untrusted` on every customer workload pod and account
   for the runtime's measured overhead in scheduling and quotas.

The operator must not label a node before the runtime and hardware checks pass.
A Firecracker-backed installation may retain the `untrusted` workload contract,
but must map its chosen handler consistently in containerd and the
`RuntimeClass`.

The declaration and rendering are unit tested. Starting a Kata or Firecracker VM
is **untested here because this host does not expose the required virtualisation
hardware**. Installing either runtime is outside this issue.

## Admission and the node store

Decision 28 places `liquid serve admission` before pod creation. It verifies the
attestation for every referenced image before Kubernetes may schedule the pod;
the microVM then limits what an admitted workload can do at runtime. Attestation
verification and VM isolation address different threats, so neither replaces the
other. The operator emits `runtimeClassName: untrusted`; the admission wiring
([admission](admission.md)) and the platform resources ([platform surface](platform-surface.md)) must reject customer workloads that
try to bypass that assignment.

`liquid node-store` stays on the worker host. The node store fetches
and verifies the chunks needed for the image tree, and its snapshotter or VFS
contract presents that root filesystem to the Kata runtime. The guest receives
only its image filesystem; it does not receive the host store, credentials, cache,
or control socket. Cache sharing therefore remains outside the guest-kernel
boundary.

## Consequences

- The Orama operator creates one profiled namespace per change, adds only the
  required network-policy exceptions, applies customer quotas, and assigns the
  `untrusted` runtime to every preview workload. Preview namespaces from one
  customer may trust each other only where explicit policy permits it.
- The [platform surface](platform-surface.md) exposes the same rules through the deployment surface. It must preserve
  the runtime assignment across deploys and refuse manifests that remove it.
- The [lifecycle runbooks](lifecycle/README.md) own lifecycle for separate customer clusters and for the Kata runtime,
  guest kernel, VM image, hardware checks, capacity, upgrades, backup, and node
  replacement.
- vcluster is not installed. Kata or Firecracker installation, the preview
  controller, and fleet lifecycle remain outside this record.
