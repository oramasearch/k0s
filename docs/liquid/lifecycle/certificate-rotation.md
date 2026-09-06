<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Certificate rotation

## What

k0s creates a Kubernetes CA, an etcd CA for managed etcd, and a service-account
key pair. Its default CAs expire after ten years. With an available signing key,
k0s renews its control-plane serving certificates. Kubelet client certificates
rotate through the Kubernetes certificate API; kubelet serving certificates use
the certificate-signing workflow when serving rotation is enabled.

`hack/lifecycle/rotate-certs.sh` performs the disruptive Kubernetes CA and
service-account key rotation described here: it stops the cluster, preserves the
old Kubernetes credentials under `/var/lib/k0s/lifecycle-certificate-rotation`,
starts one controller to generate the new credentials, distributes them to the
other controllers, bootstraps workers with a short-lived token, and validates
health. It intentionally does not rotate the managed-etcd CA; that has a
different quorum-sensitive procedure.

The script requires `K0S_LIFECYCLE_CA_ROTATION_BACKUP` to name a verified k0s
backup archive before it starts.

## Why

Replacing a root CA invalidates every certificate it signed. Rolling serving
certificates with the existing CA is routine; replacing the CA is a coordinated
recovery operation that requires all controllers and workers to trust the same
new root. Keeping the managed-etcd CA unchanged preserves etcd membership while
the Kubernetes API trust chain changes.

## How to verify

Inspect expiry dates on each controller and worker, then run the health check:

```shell
ssh controller0 'find /var/lib/k0s/pki -name "*.crt" -exec openssl x509 -noout -enddate -in {} \;'
ssh worker0 'find /var/lib/k0s/kubelet/pki -name "*.pem" -exec openssl x509 -noout -enddate -in {} \;'
hack/lifecycle/check-health.sh
```

For a planned root rotation, take and test a backup first:

```shell
hack/lifecycle/backup.sh backups/pre-ca-rotation.tar.gz
K0S_LIFECYCLE_CA_ROTATION_BACKUP=backups/pre-ca-rotation.tar.gz \
  hack/lifecycle/rotate-certs.sh
```

Verify that the API remains reachable and each worker reports Ready after the
bootstrap files are removed.

## How to change

Set `K0S_LIFECYCLE_CERTIFICATE_WARNING_SECONDS` to change the 30-day warning
threshold used by `check-health.sh`. Treat managed-etcd CA rotation as a
separate, tested maintenance procedure: back up first, preserve quorum, and
rotate its client, peer, and serving certificates together. Do not delete a CA
to force a routine serving-certificate renewal.
