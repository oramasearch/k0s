<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Backup and restore

## What

`hack/lifecycle/backup.sh <archive>` streams `k0s backup --save-path -` from a
controller to the operator workstation. `hack/lifecycle/restore.sh <archive>`
copies that archive to a fresh controller, restores it, installs and starts the
controller, and optionally rejoins the other inventory controllers when
`K0S_LIFECYCLE_REJOIN_CONTROLLERS=true`.

`k0s backup` covers the etcd or Kine snapshot, PKI, k0s configuration,
manifests, image bundles, Helm configuration, and therefore the declared
component `Chart` resources. It does not cover PersistentVolume contents or an
external datastore.

## Why

Cluster state and application data fail differently and must have separate
recovery paths. Local-path volumes are tied to their node; recover their data
through the application or a node backup. Longhorn volumes use Longhorn's own
snapshot and backup target features. A restore from a k0s archive alone restores
the Kubernetes objects that refer to a volume, not its contents.

If PersistentVolumeClaims exist, `backup.sh` requires
`K0S_LIFECYCLE_STORAGE_BACKUP_COMMAND`. Configure it to invoke the selected
storage provider's tested snapshot/backup workflow. Configure
`K0S_LIFECYCLE_STORAGE_RESTORE_COMMAND` for the corresponding restore action.
The scripts run these commands on the selected controller so credentials stay
with the cluster operator.

## How to verify

Take an archive and validate it before relying on it:

```shell
backup=backups/k0s-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
K0S_LIFECYCLE_STORAGE_BACKUP_COMMAND='/usr/local/sbin/longhorn-backup' \
  hack/lifecycle/backup.sh "$backup"
tar tzf "$backup" | head
```

On isolated replacement controllers, stop k0s and run `k0s reset` before the
restore. Restore the archive, then verify workloads, component versions, and
volume data from the storage provider's backup:

```shell
K0S_LIFECYCLE_REJOIN_CONTROLLERS=true \
K0S_LIFECYCLE_STORAGE_RESTORE_COMMAND='/usr/local/sbin/longhorn-restore' \
  hack/lifecycle/restore.sh "$backup"
hack/lifecycle/check-health.sh
```

The control-plane load-balancer address in the restored configuration must stay
the same so existing workers can reconnect.

## How to change

Keep archives encrypted and off-cluster, test a restore on fresh controllers,
and retain storage-provider backups on an independently protected target. To
change storage provider, migrate claims and their data before setting the
replaced liquid component to `external`; the k0s archive is not a volume
migration tool.
