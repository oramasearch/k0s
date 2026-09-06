// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidsnapshotter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	containersnapshots "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

const (
	targetSnapshotRefLabel = "containerd.io/snapshot.ref"
	liquidTreeLabel        = "containerd.io/snapshot/liquid.tree"
	liquidStoreLabel       = "containerd.io/snapshot/liquid.store"
	liquidPathLabel        = "containerd.io/snapshot/liquid.path"
)

type delegateProvider func(context.Context) (snapshots.Snapshotter, error)

// Snapshotter dispatches stock snapshots to overlayfs and liquid images to the node store.
type Snapshotter struct {
	delegate delegateProvider
	resolver contractResolver
	store    nodeStore

	mountMu sync.Mutex
	mounts  map[string]string
}

// New creates a liquid dispatch snapshotter.
func New(delegate delegateProvider, resolver contractResolver, store nodeStore) *Snapshotter {
	return &Snapshotter{
		delegate: delegate,
		resolver: resolver,
		store:    store,
		mounts:   make(map[string]string),
	}
}

func (s *Snapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return snapshots.Info{}, err
	}
	return delegate.Stat(namespaced(ctx), key)
}

func (s *Snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return snapshots.Info{}, err
	}
	return delegate.Update(namespaced(ctx), info, fieldpaths...)
}

func (s *Snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return snapshots.Usage{}, err
	}
	return delegate.Usage(namespaced(ctx), key)
}

func (s *Snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return nil, err
	}
	ctx = namespaced(ctx)
	info, err := delegate.Stat(ctx, key)
	if err != nil {
		return nil, err
	}
	if isLiquid(info) && info.Kind == snapshots.KindCommitted {
		path, err := s.ensureMounted(ctx, info.Labels[liquidTreeLabel], info.Labels[liquidStoreLabel])
		if err != nil {
			return nil, err
		}
		return liquidBindMount(path), nil
	}
	mounts, err := delegate.Mounts(ctx, key)
	if err != nil {
		return nil, err
	}
	return s.addLiquidRoot(ctx, mounts, info)
}

func (s *Snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	labels, err := labelsFromOpts(opts)
	if err != nil {
		return nil, err
	}
	if manifestDigest := labels[containersnapshots.TargetManifestDigestLabel]; manifestDigest != "" {
		contract, found, resolveErr := s.resolver.Resolve(namespaced(ctx), manifestDigest)
		if found {
			return s.prepareLiquid(ctx, key, parent, labels, contract, resolveErr)
		}
		if resolveErr != nil {
			log.G(ctx).WithError(resolveErr).Warn("Unable to inspect image for liquid annotations; using overlayfs")
		}
	}

	delegate, err := s.delegate(ctx)
	if err != nil {
		return nil, err
	}
	ctx = namespaced(ctx)
	parentInfo, err := parentLiquidInfo(ctx, delegate, parent)
	if err != nil {
		return nil, err
	}
	if isLiquid(parentInfo) {
		labels = mergeLiquidLabels(labels, parentInfo.Labels)
		opts = []snapshots.Opt{snapshots.WithLabels(labels)}
	}
	mounts, err := delegate.Prepare(ctx, key, parent, opts...)
	if err != nil {
		return nil, err
	}
	return s.addLiquidRoot(ctx, mounts, parentInfo)
}

func (s *Snapshotter) prepareLiquid(
	ctx context.Context,
	key string,
	parent string,
	labels map[string]string,
	contract imageContract,
	contractErr error,
) ([]mount.Mount, error) {
	if contractErr != nil {
		return s.fallbackOrFail(ctx, key, parent, labels, contract, contractErr)
	}
	target := labels[targetSnapshotRefLabel]
	if target == "" {
		return s.fallbackOrFail(ctx, key, parent, labels, contract, errors.New("containerd supplied no target snapshot reference"))
	}

	delegate, err := s.delegate(ctx)
	if err != nil {
		return nil, err
	}
	ctx = namespaced(ctx)

	if existing, err := delegate.Stat(ctx, target); err == nil {
		tree := existing.Labels[liquidTreeLabel]
		if tree == "" && contract.HasLayers {
			return nil, fmt.Errorf("%w: overlay snapshot %s is ready", errdefs.ErrAlreadyExists, target)
		}
		if tree != contract.Tree {
			return nil, fmt.Errorf("%w: snapshot %s belongs to liquid tree %s", errdefs.ErrFailedPrecondition, target, tree)
		}
		path, err := s.ensureMounted(ctx, contract.Tree, contract.StoreURL)
		if err != nil {
			return s.fallbackOrFail(ctx, key, parent, labels, contract, err)
		}
		liquidLabels := liquidSnapshotLabels(labels, contract, path)
		existing.Labels = mergeLiquidLabels(existing.Labels, liquidLabels)
		if _, err := delegate.Update(ctx, existing, "labels"); err != nil {
			return nil, fmt.Errorf("label existing liquid snapshot: %w", err)
		}
		return nil, fmt.Errorf("%w: liquid snapshot %s is ready", errdefs.ErrAlreadyExists, target)
	} else if !errdefs.IsNotFound(err) {
		return nil, err
	}
	path, err := s.ensureMounted(ctx, contract.Tree, contract.StoreURL)
	if err != nil {
		return s.fallbackOrFail(ctx, key, parent, labels, contract, err)
	}
	liquidLabels := liquidSnapshotLabels(labels, contract, path)

	if _, err := delegate.Prepare(ctx, key, parent, snapshots.WithLabels(liquidLabels)); err != nil {
		return nil, err
	}
	if err := delegate.Commit(ctx, target, key, snapshots.WithLabels(liquidLabels)); err != nil {
		removeErr := delegate.Remove(ctx, key)
		return nil, errors.Join(err, removeErr)
	}
	return nil, fmt.Errorf("%w: liquid snapshot %s is ready", errdefs.ErrAlreadyExists, target)
}

func liquidSnapshotLabels(labels map[string]string, contract imageContract, path string) map[string]string {
	return mergeLiquidLabels(labels, map[string]string{
		liquidTreeLabel:  contract.Tree,
		liquidStoreLabel: contract.StoreURL,
		liquidPathLabel:  path,
	})
}

func (s *Snapshotter) fallbackOrFail(
	ctx context.Context,
	key string,
	parent string,
	labels map[string]string,
	contract imageContract,
	cause error,
) ([]mount.Mount, error) {
	if !contract.HasLayers {
		return nil, fmt.Errorf("%w: mount liquid tree %s from %s: %w", errdefs.ErrUnavailable, contract.Tree, contract.StoreURL, cause)
	}
	log.G(ctx).WithError(cause).Warn("Liquid store unavailable; pulling image layers with overlayfs")
	delegate, err := s.delegate(ctx)
	if err != nil {
		return nil, err
	}
	return delegate.Prepare(namespaced(ctx), key, parent, snapshots.WithLabels(labels))
}

func (s *Snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return nil, err
	}
	ctx = namespaced(ctx)
	parentInfo, err := parentLiquidInfo(ctx, delegate, parent)
	if err != nil {
		return nil, err
	}
	if isLiquid(parentInfo) {
		labels, err := labelsFromOpts(opts)
		if err != nil {
			return nil, err
		}
		labels = mergeLiquidLabels(labels, parentInfo.Labels)
		opts = []snapshots.Opt{snapshots.WithLabels(labels)}
	}
	mounts, err := delegate.View(ctx, key, parent, opts...)
	if err != nil {
		return nil, err
	}
	return s.addLiquidRoot(ctx, mounts, parentInfo)
}

func (s *Snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return err
	}
	return delegate.Commit(namespaced(ctx), name, key, opts...)
}

func (s *Snapshotter) Remove(ctx context.Context, key string) error {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return err
	}
	ctx = namespaced(ctx)
	if err := delegate.Remove(ctx, key); err != nil {
		return err
	}
	return s.releaseUnused(ctx, delegate)
}

func (s *Snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return err
	}
	return delegate.Walk(namespaced(ctx), fn, filters...)
}

func (s *Snapshotter) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.mountMu.Lock()
	mounted := s.mounts
	s.mounts = make(map[string]string)
	s.mountMu.Unlock()

	var errs []error
	for tree := range mounted {
		if err := s.store.Unmount(ctx, tree); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Snapshotter) Cleanup(ctx context.Context) error {
	delegate, err := s.delegate(ctx)
	if err != nil {
		return err
	}
	cleaner, ok := delegate.(snapshots.Cleaner)
	if !ok {
		return errdefs.ErrNotImplemented
	}
	return cleaner.Cleanup(namespaced(ctx))
}

func (s *Snapshotter) ensureMounted(ctx context.Context, tree, storeURL string) (string, error) {
	s.mountMu.Lock()
	defer s.mountMu.Unlock()
	if path := s.mounts[tree]; path != "" {
		return path, nil
	}
	path, err := s.store.Mount(ctx, tree, storeURL)
	if err != nil {
		return "", err
	}
	s.mounts[tree] = path
	return path, nil
}

func (s *Snapshotter) addLiquidRoot(ctx context.Context, mounts []mount.Mount, info snapshots.Info) ([]mount.Mount, error) {
	if !isLiquid(info) {
		return mounts, nil
	}
	path, err := s.ensureMounted(ctx, info.Labels[liquidTreeLabel], info.Labels[liquidStoreLabel])
	if err != nil {
		return nil, err
	}
	for index := range mounts {
		mounts[index] = addLowerDirectory(mounts[index], path)
	}
	return mounts, nil
}

func (s *Snapshotter) releaseUnused(ctx context.Context, delegate snapshots.Snapshotter) error {
	used := make(map[string]struct{})
	if err := delegate.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		if tree := info.Labels[liquidTreeLabel]; tree != "" {
			used[tree] = struct{}{}
		}
		return nil
	}); err != nil {
		return err
	}

	s.mountMu.Lock()
	defer s.mountMu.Unlock()
	var errs []error
	for tree := range s.mounts {
		if _, ok := used[tree]; ok {
			continue
		}
		if err := s.store.Unmount(ctx, tree); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(s.mounts, tree)
	}
	return errors.Join(errs...)
}

func labelsFromOpts(opts []snapshots.Opt) (map[string]string, error) {
	var info snapshots.Info
	for _, opt := range opts {
		if err := opt(&info); err != nil {
			return nil, err
		}
	}
	if info.Labels == nil {
		return make(map[string]string), nil
	}
	return info.Labels, nil
}

func parentLiquidInfo(ctx context.Context, delegate snapshots.Snapshotter, parent string) (snapshots.Info, error) {
	if parent == "" {
		return snapshots.Info{}, nil
	}
	return delegate.Stat(ctx, parent)
}

func isLiquid(info snapshots.Info) bool {
	return info.Labels[liquidTreeLabel] != ""
}

func mergeLiquidLabels(labels, liquidLabels map[string]string) map[string]string {
	merged := make(map[string]string, len(labels)+3)
	maps.Copy(merged, labels)
	for _, key := range []string{liquidTreeLabel, liquidStoreLabel, liquidPathLabel} {
		if value := liquidLabels[key]; value != "" {
			merged[key] = value
		}
	}
	return merged
}

func liquidBindMount(path string) []mount.Mount {
	return []mount.Mount{{
		Type:    "bind",
		Source:  path,
		Options: []string{"rbind", "ro"},
	}}
}

func addLowerDirectory(original mount.Mount, path string) mount.Mount {
	if original.Type == "bind" {
		return mount.Mount{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"workdir=" + filepath.Join(filepath.Dir(original.Source), "work"),
				"upperdir=" + original.Source,
				"lowerdir=" + path,
			},
		}
	}
	if original.Type != "overlay" {
		return original
	}
	for index, option := range original.Options {
		if lowerDirectories, found := strings.CutPrefix(option, "lowerdir="); found {
			original.Options[index] = "lowerdir=" + path + ":" + lowerDirectories
			return original
		}
	}
	original.Options = append(original.Options, "lowerdir="+path)
	return original
}
