// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidsnapshotter

import (
	"context"
	"fmt"
	"sync"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

type containerdRuntime struct {
	address string

	mu       sync.Mutex
	client   *containerd.Client
	delegate snapshots.Snapshotter
	resolver contractResolver
}

func (r *containerdRuntime) snapshotter(context.Context) (snapshots.Snapshotter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.delegate != nil {
		return r.delegate, nil
	}

	client, err := containerd.New(r.address)
	if err != nil {
		return nil, fmt.Errorf("connect to containerd: %w", err)
	}
	r.client = client
	r.delegate = client.SnapshotService("overlayfs")
	r.resolver = contentContractResolver{store: client.ContentStore()}
	return r.delegate, nil
}

func (r *containerdRuntime) Resolve(ctx context.Context, manifestDigest string) (imageContract, bool, error) {
	if _, err := r.snapshotter(ctx); err != nil {
		return imageContract{}, false, err
	}
	r.mu.Lock()
	resolver := r.resolver
	r.mu.Unlock()
	return resolver.Resolve(namespaces.WithNamespace(ctx, "k8s.io"), manifestDigest)
}

func (r *containerdRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		return nil
	}
	err := r.client.Close()
	r.client = nil
	r.delegate = nil
	r.resolver = nil
	return err
}

func namespaced(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, "k8s.io")
}
