<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Platform surface

The liquid developer path presents deploy, logs, environment values, and domains without requiring
Kubernetes knowledge. The resulting resources remain ordinary Kubernetes objects that an operator can
inspect. Lake owns the page and capability for each action; orama-operator owns application
reconciliation; k0s supplies the cluster services and policy described here.

## Action contracts

| Action | Lake capability | Kubernetes representation | Lake page responsibility |
|---|---|---|---|
| Deploy | `create_deployment`; `list_deployments` and `get_environment` observe status | A desired deployment becomes `LakeEnvironment`, then a Deployment, Service, and route. | Select a repository change and environment, submit it, and show rollout status and URL. |
| Logs | `environment_logs` | The `liquid-log-shipper` Chart creates a Vector DaemonSet that sends pod logs to a Loki-compatible endpoint. | Page through `{ts,pod,container,line}` records using the returned cursor. |
| Environment | `set_environment_env` and `unset_environment_env` | The operator writes plain values to `LakeEnvironment.spec.env` and sealed references to `spec.secretRefs`. | Edit plain configuration and select lake secret or dynamic-secret references without displaying delivered values. |
| Domains | `create_domain` and `delete_domain` | `LakeDomain { environment, host, tls }` becomes an `HTTPRoute`, a listener on the cluster Gateway, and optionally a cert-manager `Certificate`. | Validate the host and show route and certificate readiness. |

The Lake capabilities authorize against the repository environment object and use the normal
`{ok,value|error,duration_ms}` envelope. `create_deployment` appends the same desired deployment record
used by `liquid deploy`; Lake does not create workload resources itself. The Lake and operator companion
changes own those handlers, pages, CRDs, and reconcilers.

`LakeDomain` is the operator-owned namespaced contract:

```yaml
apiVersion: orama.cloud/v1alpha1
kind: LakeDomain
metadata:
  name: production-example-com
spec:
  environment: production
  host: example.com
  tls: acme # acme or provided
```

For `tls: acme`, the reconciler creates a `Certificate` using `ClusterIssuer/lake-acme`, adds a
hostname-specific HTTPS listener whose certificate reference names the resulting Secret, and attaches
the `HTTPRoute` to that listener. `tls: provided` references an operator-selected TLS Secret instead.
The route backend is the Service owned by the referenced `LakeEnvironment`.

## Cluster configuration

New liquid configurations declare cert-manager and Vector beside Cilium and the Gateway API CRDs.
The platform endpoints are cluster configuration, not application environment values:

```yaml
spec:
  liquid:
    platform:
      acmeDirectory: https://lake.example/acme/customer-id/directory
      logsEndpoint: http://loki.liquid-system.svc.cluster.local:3100
```

`acmeDirectory` is optional until the Lake customer authority exists. When set, it must be the exact
RFC 8555 directory for that customer. k0s writes `ClusterIssuer/lake-acme`; its account key is stored in
`Secret/lake-acme-account` by cert-manager. cert-manager uses an HTTP-01 `gatewayHTTPRoute` solver
attached to `liquid-system/liquid` on port 80.

`logsEndpoint` is the Loki base URL used by both Vector and Lake. Vector adds the Loki push path. Lake
adds `/loki/api/v1/query_range` and queries by the environment namespace. The default stable Service
name lets a Lake installation or customer-supplied log service own the storage backend without making
Loki another k0s component. Stream labels are fixed as `namespace`, `pod`, and `container`.

The two endpoint fields accept HTTP for in-cluster services and HTTPS for remote services. Query strings
and fragments are refused so credentials cannot be embedded in the cluster configuration. Endpoint
authentication must use a separately delivered credential and is not part of this issue.

## Gateway defaults

k0s writes `GatewayClass/liquid` with Cilium controller name `io.cilium/gateway-controller` and the
default `Gateway/liquid` in `liquid-system`. Its port 80 listener accepts routes from every namespace so
cert-manager can create temporary HTTP-01 routes and environment namespaces can attach application
routes. Domain reconciliation adds and removes hostname-specific HTTPS listeners; the shared HTTP
listener remains stable.

Replacing Cilium or the Gateway API CRDs requires an implementation that reconciles this GatewayClass,
preserves the listener and cross-namespace route contract, and reports standard Gateway conditions.

## Logs shortcut

`k0s liquid logs <namespace>/<pod>` reads the stored `logsEndpoint` and issues the same Loki query used
by `environment_logs`, adding both namespace and pod labels. It prints timestamp, container, and line in
chronological order. This is an operator shortcut and uses the controller admin kubeconfig only to read
the cluster configuration. An in-cluster Service endpoint is reached through the authenticated
Kubernetes Service proxy; an external endpoint is queried directly. The command does not call the
Kubernetes pod log API.

## Environment policy

Every environment namespace carries `liquid.oramasearch.com/environment=true`, Pod Security Standards
`restricted`, a default-deny `NetworkPolicy`, `ResourceQuota`, and `LimitRange`. The profile is rendered
with `k0s liquid tenant-profile <namespace>` and is also the contract used by the environment controller.
It also contains the namespaced `Role/liquid-tenant` and `RoleBinding/liquid-tenant`. This profile is
the single definition of tenant access, instantiated once in each environment namespace.

### Tenant RBAC is authoritative

The RoleBinding grants access to the authenticated group `liquid:tenant:<namespace>`. The namespace
argument determines that group name, so rendering the existing command needs no additional arguments.
The trusted identity provider must assign membership only to identities authorized for that environment;
the profile defines authorization and does not issue credentials or create users. Workload service
accounts, including `default`, are not members of this group and receive no grant from this binding.

| Resources | Tenant verbs |
|---|---|
| Pods, Services, ConfigMaps, PersistentVolumeClaims | get, list, watch, create, update, patch, delete |
| Deployments, StatefulSets (`apps`) | get, list, watch, create, update, patch, delete |
| Jobs, CronJobs (`batch`) | get, list, watch, create, update, patch, delete |
| ReplicaSets (`apps`), Events, ResourceQuotas, LimitRanges | get, list, watch |
| Pod logs | get |

There are no wildcards and **no Secret permissions**, including read, watch, create, update, patch,
or deletion. There is no access to RBAC objects, service accounts or their tokens, impersonation,
Pod exec/attach/proxy/port-forward or ephemeral containers, network-policy writes, quota/limit writes,
namespace writes, or cluster resources. Separate scale subresources, autoscalers, DaemonSets,
routes, certificates, and custom-resource writes are also omitted. The operator retains domain,
network, quota, credential, and environment reconciliation. Add a permission only when a concrete
tenant workflow requires it; the initial grant deliberately favors the smaller set.

**RBAC is the authoritative control for tenant Secret access** (liquid-bridge decision 38).
The profile's binding must be the only grant tenant identities receive in an environment namespace.
Kubernetes RBAC is additive: an extra RoleBinding or ClusterRoleBinding, including an `edit` or
`admin` grant, can invalidate this boundary. RBAC cannot select namespaces by label; the namespaced
Role and binding must be installed with every environment profile. Do not place privileged service
accounts in tenant namespaces: a tenant allowed to create Pods can choose a local service account.

`system:serviceaccount:orama-system:orama-operator` retains its separately managed Secret delivery
permissions. This tenant grant neither replaces nor removes the operator's RBAC. Under this access
contract, tenant requests cannot reach Secret admission, even when their label and owner reference
are forged. Cluster administrators remain trusted to maintain this contract and must not add other
Secret writers in environment namespaces. Omitting Secret API read does not prevent a workload from
consuming a delivered Secret in its own namespace; this is an environment access boundary, not
isolation between workloads in one trusting environment.

### Admission is defence in depth

Application configuration has two sources only:

- `LakeEnvironment.spec.env` contains plain name/value entries.
- `LakeEnvironment.spec.secretRefs` names `LakeSealedSecret` resources. The operator resolves each
  reference and creates a Secret with controller ownership and `lake.orama.cloud/sealed=true`.

k0s keeps both shipped `ValidatingAdmissionPolicy` objects unchanged. `liquid-environment-secrets`
checks that a Secret CREATE or UPDATE carries a controller owner reference with kind `LakeSealedSecret`
and API version `orama.cloud/v1alpha1`, plus `lake.orama.cloud/sealed=true`.
`liquid-environment-pods` checks that Secret names used by Pod volumes and container environment
values appear in `liquid.oramasearch.com/sealed-secret-names`. The operator derives that annotation
from `spec.secretRefs`; it is not an environment setting exposed by Lake.

These are structural checks on submitted fields. They do **not** look up the claimed owner, verify
its existence or UID, establish actual controller ownership, look up referenced Secrets, or authenticate
the annotation's author. An authorized Secret writer can submit a nonexistent owner; a tenant can
author the Pod annotation. Admission catches missing delivery metadata from an authorized writer;
it does not prove provenance. RBAC prevents the tenant from creating the forged Secret in the first
place. Owner validation and an unforgeable Pod annotation are not claimed by these policies.

## Ownership outside k0s

The Lake companion change owns the four pages and capability handlers. The orama-operator companion
change owns `LakeDomain`, environment and domain reconciliation, and the annotation derived from sealed
references. Deploy record creation, the sealed envelope, and their existing client/operator contracts
remain owned by liquid #84 and orama-operator #129.

## Verification

The native proof runs without containers:

```sh
GOMAXPROCS=6 GOFLAGS=-p=6 go build ./...
GOMAXPROCS=6 GOFLAGS=-p=6 go vet ./...
GOMAXPROCS=6 GOFLAGS=-p=6 go test ./static ./cmd/liquid
GOMAXPROCS=6 GOFLAGS=-p=6 go test -tags envtest -count=1 -v ./static -run '^TestTenantProfileRBAC$'
```

The `envtest` tag enables the live API-server proof; it is separate from unit tests because it needs
native `kube-apiserver` and `etcd` binaries. Set `KUBEBUILDER_ASSETS` to their directory, or the test
downloads Kubernetes 1.30.3 assets into a temporary directory. It always starts an isolated cluster
with RBAC enabled, installs the rendered profile and both shipped admission policies with their
bindings, and authenticates the tenant by a client certificate in the actual RoleBinding's group.
The admin client only bootstraps fixtures, including quota status because envtest has no quota
controller. The operator uses a real service-account token and a separate minimal fixture Role
granting Secret get/create/update; its production RBAC remains owned by orama-operator.

The proof refuses the issue #40 forgery (`database`, sealed label, nonexistent `LakeSealedSecret`
controller reference), plain Secret creation, and tenant Secret reads and mutations. The same forged
payload can be created and updated by the operator, deliberately demonstrating the admission limit.
It verifies both admission policies are active with refused dry-run requests, checks ordinary tenant
ConfigMap lifecycle, restricted `untrusted` Pod create/read/delete, Service creation and quota reads,
and checks authorization denials for Secret watch, escalation, policy changes, and access outside the
namespace. Envtest verifies API authorization and admission, not workload scheduling, microVM startup,
or the operator's reconciliation loop (orama-operator #155).

`make check-liquid-platform` starts one bootloose controller-worker with the declared components. It
runs Pebble v2.10.1 as the ACME double and Loki v3.7.7 as the customer log endpoint, obtains a
certificate through the Gateway API HTTP-01 solver, reaches an application `HTTPRoute` over TLS, reads
the application's startup line through Vector and `k0s liquid logs`, and verifies that a Pod mounting a
manually created Secret is refused in an environment namespace. The target is part of the smoketest
list. The unit suite covers the log query, in-cluster Service proxy, response formatting, and command
routing.
