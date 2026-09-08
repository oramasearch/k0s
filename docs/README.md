<!--
SPDX-FileCopyrightText: 2020 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# k0s - The Zero Friction Kubernetes

<!-- When changing this file, consider to change ../README.md, too! -->

![k0s-logo-dark](img/k0s-logo-2025-horizontal-inverted.svg#only-dark)
![k0s-logo-light](img/k0s-logo-2025-horizontal.svg#only-light)

{%
    include-markdown "../README.md"
    start="<!-- Start Overview -->"
    end="<!-- End Overview -->"
%}

{%
    include-markdown "../README.md"
    start="<!-- Start Key Features -->"
    end="<!-- End Key Features -->"
%}

## Getting Started

### Liquid distribution

This private copy distributes liquid k0s. Start with these resources when you
need the liquid components and Lake installation path:

- [Liquid distribution guide](liquid/README.md) - Identify the `liquid`
  branch, its `k0s` artifact, and the Linux runtime requirement.
- [Lake on one k0s node](liquid/lake-on-k0s.md) - Install Lake on a connected
  Linux host. The runbook's host validation remains deferred.
- [Air-gapped Lake installation (orama-operator repository)](https://github.com/oramasearch/orama-operator/blob/main/docs/install/k0s.md) - Transfer and install Lake in an air-gapped environment. The runbook's Linux-host validation remains deferred.

### Upstream k0s

- [Quick Start Guide](install.md) for creating a full Kubernetes cluster with a single node.
- [Install using k0sctl](k0sctl-install.md) - Deploy and upgrade multi-node clusters with one command.
- [Run k0s in Docker](k0s-in-docker.md) - Run k0s controllers and workers in containers.

## Demo

![k0s demo](img/k0s_demo.gif)

{%
    include-markdown "../README.md"
    start="<!-- Start Join the Community -->"
    end="<!-- End Join the Community -->"
%}

<!-- Copied over from "Join the Community" because rewriting the link won't work here. -->
### Adopters

k0s is used across diverse environments, from small-scale far-edge deployments
to large data centers. Share your use case and add yourself to the list of
[adopters].

[adopters]: adopters.md

## Commercial Support

[Mirantis](https://www.mirantis.com/software/k0s/) offers technical support, professional services and training for k0s. The support subscriptions include, for example, prioritized support (Phone, Web, Email) and access to verified extensions on top of your k0s cluster.

For any k0s inquiries, please contact us via email [info@k0sproject.io](mailto:info@k0sproject.io).

{%
    include-markdown "../README.md"
    start="<!-- Start Motivation -->"
    end="<!-- End Motivation -->"
%}

{%
    include-markdown "../README.md"
    start="<!-- Start Scope -->"
    end="<!-- End Scope -->"
%}
