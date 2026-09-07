<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Liquid image admission

k0s routes Pod admission through `liquid serve admission`. A Pod is allowed only when
every image in its containers, init containers, and ephemeral containers is pinned by
digest and has a valid TAAP attestation from an accepted issuer and profile. The liquid
process performs all attestation verification through libliquid. k0s does not duplicate
canonical CBOR, signature, profile, or transparency verification.

## Policy and routing

`spec.liquid.admission` is the cluster default. It has `mode`, `issuers`, `profiles`,
`exempt`, `attestationStore`, `log`, and `failurePolicy`. `failurePolicy` is always `Fail`.
The default is `mode: off`, which is audit-only and lets existing clusters adopt the
feature without changing workload admission. An enforcing policy requires an
`attestationStore` URL only when the worker node store is disabled.

`AdmissionPolicy` is a namespaced CRD at
`admissionpolicies.liquid.k0sproject.io`. It has the same policy fields and replaces the
cluster default for Pods in its namespace. There is one policy object per namespace; k0s
rejects an ambiguous namespace during reconciliation. A namespace that needs lenient
operation uses `mode: "off"`, rather than weakening webhook transport failure handling.

k0s renders the default and each namespace policy into the policy directory consumed by
liquid. A namespace policy named `strict` in `customer-a` is rendered as policy set
`customer-a--strict`; the adapter maps Pods from that namespace to
`POST /admit/customer-a--strict`. All other Pods use `POST /admit/default`.

```yaml
apiVersion: liquid.k0sproject.io/v1alpha1
kind: AdmissionPolicy
metadata:
  name: strict
  namespace: customer-a
spec:
  mode: attestation+log
  issuers:
  - taap:1:ed25519:accepted-issuer
  profiles:
  - image_provenance/1
  exempt: []
  failurePolicy: Fail
  attestationStore: https://attestations.example
  log:
    mode: verify
    lake: https://lake.example
    trust:
      identity:
        taapID: taap:1:ed25519:lake-production
        publicKey: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      witnessThreshold: 1
      witnesses:
      - endpoint: https://witness.example/checkpoint
        publicKey: abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
    maximumCheckpointAgeSeconds: 300
```

`attestation+log` policy sets select `log.mode: verify` to require a valid Lake
checkpoint signature, every entry and parent tile through that checkpoint, and an
inclusion proof for the exact attestation. `log.trust.identity` pins Lake's Ed25519
TAAP identity and public key. `log.trust.witnesses` pins each witness endpoint and key;
`witnessThreshold` cannot exceed that list. Public keys are 32-byte lowercase
hexadecimal values. k0s renders these fields into liquid's strict policy JSON without
using Lake responses as trust anchors.

For an immediate fail-closed fallback, set `log.mode: deny`. Liquid refuses every
otherwise-valid log-mode attestation with `log verification denied by policy`; it does
not weaken the policy to unverified inclusion. To operate without log verification,
make the separate policy choice to use `mode: attestation` or `mode: off`.

## Deployment and certificates

Each Linux worker runs a `kube-system/liquid-admission` static Pod. Its `liquid`
container bind-mounts the liquid executable embedded in the k0s distribution. Its
`adapter` container bind-mounts the k0s executable, terminates the Kubernetes webhook
connection, selects the named liquid policy, and forwards the unchanged
`AdmissionReview` to the local liquid listener.

The static Pods carry the `liquid-admission` label. The k0s-managed Service selects them,
and the cluster-scoped `ValidatingWebhookConfiguration` calls that Service at
`/admission/review` with `failurePolicy: Fail`, a ten-second timeout, and a namespace
selector that excludes `kube-system`. This exemption permits the static Pod and other
essential cluster services to start. The verifier namespace is therefore never subject
to its own webhook.

k0s issues `liquid-admission.crt` and `liquid-admission.key` from its cluster CA with
the Service DNS names as subject alternative names. The controller copies that material
and `ca.crt` into the `liquid-admission-tls` Secret for the static Pod and uses the same
CA bytes in the webhook configuration. Certificate renewal therefore follows the normal
k0s certificate path.

## Attestation source and offline operation

The policy's `attestationStore` is passed to liquid as `attestation_source`. When the
worker node store provides `GET /v1/attestations?subject=<digest>`, it is the preferred
source. A policy URL remains the fallback where that endpoint is not available.

In `attestation+log` mode, liquid owns the log verifier. With `log.mode: verify`, it
checks Lake's checkpoint signature and witness threshold against the policy trust set,
authenticates every entry and parent tile to that checkpoint, and then verifies the
checkpoint-bound inclusion proof. The cache retains inspection data and immutable tile
bytes, but every use reauthenticates the complete path. A missing or forged tile, stale
checkpoint, invalid proof, or unavailable source is a refusal; the verifier never
assumes inclusion. Tile failures retain their level, index, and width in the decision.

## Failure handling and events

The webhook is fail closed. Transport failures, an unavailable liquid process, invalid
policy data, an unavailable attestation source, and verifier failures deny the Pod.
Liquid returns the affected image and a stable reason: `no attestation`, `issuer not
accepted`, `signature invalid`, `not in log`, `checkpoint stale`, or `log verifier
unavailable`.

Liquid publishes its structured decisions at `GET /v1/decisions`. The colocated k0s
adapter associates each sequence number with the namespace from its admission request
and creates a Kubernetes Event there. Allowed decisions use `LiquidAdmissionAllowed`;
denied decisions use `LiquidAdmissionDenied` and include the image and liquid reason.

## Liquid contract

The embedded liquid revision supplies:

- `POST /admit/<name>` for Kubernetes `AdmissionReview` v1 requests and responses;
- `GET /v1/policy?name=<name>` to inspect an effective policy;
- `GET /v1/verify?policy=<name>&image=<digest>` for an operator decision query;
- `GET /v1/decisions` for the adapter's structured decision stream; and
- `liquid_admission_reviews_total{decision,reason}` metrics.

The liquid verifier filters issuer and profile before accepting an attestation and uses
the `verify_attestation` path. For `attestation+log` policy sets in verify mode, it uses
liquid's shared checkpoint, tile-path, and inclusion verifier from liquid #83 and #268.

## Verification

Unit tests cover policy resolution, verify-mode policy rendering, the documented
deny-mode fallback, webhook CA rendering, and the adapter's namespace route. The
adapter test forwards both an accepted decision with verified inclusion evidence and a
forged-tile refusal that names the tile. The container-backed admission flow is deferred
on this host because Docker is unavailable.
