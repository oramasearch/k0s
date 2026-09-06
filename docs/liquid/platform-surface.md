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

Application configuration has two sources only:

- `LakeEnvironment.spec.env` contains plain name/value entries.
- `LakeEnvironment.spec.secretRefs` names `LakeSealedSecret` resources. The operator resolves each
  reference and creates a Secret with controller ownership and `lake.orama.cloud/sealed=true`.

k0s installs two `ValidatingAdmissionPolicy` objects when liquid is configured. The first refuses Secret
creation or update in an environment namespace unless a `LakeSealedSecret` controller owns it and the
sealed-delivery label is present. The second refuses Pod Secret volumes and Secret-backed environment
values unless the pod template annotation `liquid.oramasearch.com/sealed-secret-names` lists the resolved
Secret name. The operator writes that annotation from `spec.secretRefs`; users never author it as an
environment setting. Together the policies prevent a hand-created Secret from entering a new
environment or being consumed by its pods. If the admission component from k0s #15 is installed later,
it may enforce the same contract with live object lookup and replace these policies only after an
equivalent denial test passes.

## Ownership outside k0s

The Lake companion change owns the four pages and capability handlers. The orama-operator companion
change owns `LakeDomain`, environment and domain reconciliation, and the annotation derived from sealed
references. Deploy record creation, the sealed envelope, and their existing client/operator contracts
remain owned by liquid #84 and orama-operator #129.

## Verification

`make check-liquid-platform` starts one bootloose controller-worker with the declared components. It
runs Pebble v2.10.1 as the ACME double and Loki v3.7.7 as the customer log endpoint, obtains a
certificate through the Gateway API HTTP-01 solver, reaches an application `HTTPRoute` over TLS, reads
the application's startup line through Vector and `k0s liquid logs`, and verifies that a Pod mounting a
manually created Secret is refused in an environment namespace. The target is part of the smoketest
list. The unit suite covers the log query, in-cluster Service proxy, response formatting, and command
routing.
