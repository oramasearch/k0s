<!--
SPDX-FileCopyrightText: 2026 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Observability

## What

Install controllers with `--enable-metrics-scraper`. k0s publishes system
component metrics through `k0s-system/k0s-pushgateway`; Prometheus should scrape
that service with `honorLabels: true`. Install Prometheus and Grafana as customer
managed `Chart` resources, or use an existing observability platform. They are
not liquid default components and their versions are selected by the operator.

An HA cluster using managed etcd reports these active jobs:

- `kube-scheduler`
- `kube-controller-manager`
- `etcd`

A single-controller configuration using Kine reports `kine` instead of `etcd`.
That single-node mode is not exercised by this lifecycle scenario.

## Why

The metrics expose failures that users otherwise discover during an upgrade,
restore, node loss, or certificate outage. Collect node and controller logs too:
use the host journal or service logs, retain `/var/lib/k0s` support bundles, and
ship Kubernetes event and container logs to the same retention policy as
application logs.

## How to verify

Confirm the pushgateway and its active jobs:

```shell
ssh controller0 /usr/local/bin/k0s kc -n k0s-system get deployment k0s-pushgateway
ssh controller0 /usr/local/bin/k0s kc get --raw \
  /api/v1/namespaces/k0s-system/services/http:k0s-pushgateway:http/proxy/api/v1/metrics
```

Configure Prometheus with a `ServiceMonitor` and install a Prometheus/Grafana
pair as `Chart` resources in the cluster configuration. Pin their chart versions
and repositories according to the operator's compatibility policy.

```yaml
apiVersion: helm.k0sproject.io/v1beta1
kind: Chart
metadata:
  name: liquid-prometheus
  namespace: kube-system
spec:
  chartName: prometheus-community/prometheus
  version: "<pinned-version>"
  namespace: monitoring
  repository:
    url: https://prometheus-community.github.io/helm-charts
---
apiVersion: helm.k0sproject.io/v1beta1
kind: Chart
metadata:
  name: liquid-grafana
  namespace: kube-system
spec:
  chartName: grafana/grafana
  version: "<pinned-version>"
  namespace: monitoring
  repository:
    url: https://grafana.github.io/helm-charts
```

Alert on these lifecycle failure classes:

```yaml
groups:
  - name: liquid-lifecycle
    rules:
      - alert: LiquidNodeNotReady
        expr: kube_node_status_condition{condition="Ready",status="true"} == 0
        for: 10m
      - alert: LiquidEtcdMetricsMissing
        expr: absent(up{job="etcd"})
        for: 5m
      - alert: LiquidCertificateExpiring
        expr: min_over_time(x509_cert_not_after[1h]) - time() < 30 * 24 * 3600
        for: 15m
      - alert: LiquidAutopilotPlanStalled
        expr: time() - kube_resource_created{resource="plans.autopilot.k0sproject.io"} > 3600
        for: 30m
```

Adapt metric names to the Prometheus chart's Kubernetes-object collector. The
etcd alert applies to the HA etcd topology; use `kine` for a Kine topology.

## How to change

Change the Prometheus or Grafana `Chart` version through the normal Helm
extension workflow, independently of the k0s binary and liquid component
declarations. Validate dashboard queries and alert labels after every chart
upgrade. If using an external monitoring product, preserve the pushgateway
scrape, the three active HA jobs, and equivalent alerts and log retention.
