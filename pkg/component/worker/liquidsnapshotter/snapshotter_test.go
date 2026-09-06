// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidsnapshotter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	containersnapshots "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedResolver struct {
	contract imageContract
	found    bool
	err      error
}

func (r fixedResolver) Resolve(context.Context, string) (imageContract, bool, error) {
	return r.contract, r.found, r.err
}

type fakeNodeStore struct {
	path       string
	mountErr   error
	mounts     int
	unmounts   int
	lastTree   string
	lastSource string
}

func (s *fakeNodeStore) Mount(_ context.Context, tree, source string) (string, error) {
	s.mounts++
	s.lastTree = tree
	s.lastSource = source
	return s.path, s.mountErr
}

func (s *fakeNodeStore) Unmount(context.Context, string) error {
	s.unmounts++
	return nil
}

type fakeSnapshotter struct {
	infos  map[string]snapshots.Info
	mounts map[string][]mount.Mount
}

func newFakeSnapshotter() *fakeSnapshotter {
	return &fakeSnapshotter{
		infos:  make(map[string]snapshots.Info),
		mounts: make(map[string][]mount.Mount),
	}
}

func (s *fakeSnapshotter) Stat(_ context.Context, key string) (snapshots.Info, error) {
	info, ok := s.infos[key]
	if !ok {
		return snapshots.Info{}, errdefs.ErrNotFound
	}
	return info, nil
}

func (s *fakeSnapshotter) Update(_ context.Context, info snapshots.Info, _ ...string) (snapshots.Info, error) {
	s.infos[info.Name] = info
	return info, nil
}

func (s *fakeSnapshotter) Usage(context.Context, string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}

func (s *fakeSnapshotter) Mounts(_ context.Context, key string) ([]mount.Mount, error) {
	return append([]mount.Mount(nil), s.mounts[key]...), nil
}

func (s *fakeSnapshotter) Prepare(_ context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	info := snapshots.Info{
		Kind:   snapshots.KindActive,
		Name:   key,
		Parent: parent,
	}
	for _, opt := range opts {
		if err := opt(&info); err != nil {
			return nil, err
		}
	}
	s.infos[key] = info
	if parent == "" {
		s.mounts[key] = []mount.Mount{{Type: "bind", Source: "/overlay/snapshots/1/fs"}}
	} else {
		s.mounts[key] = []mount.Mount{{
			Type:   "overlay",
			Source: "overlay",
			Options: []string{
				"workdir=/overlay/work",
				"upperdir=/overlay/upper",
				"lowerdir=/overlay/lower",
			},
		}}
	}
	return append([]mount.Mount(nil), s.mounts[key]...), nil
}

func (s *fakeSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	mounts, err := s.Prepare(ctx, key, parent, opts...)
	if err == nil {
		info := s.infos[key]
		info.Kind = snapshots.KindView
		s.infos[key] = info
	}
	return mounts, err
}

func (s *fakeSnapshotter) Commit(_ context.Context, name, key string, opts ...snapshots.Opt) error {
	info, ok := s.infos[key]
	if !ok {
		return errdefs.ErrNotFound
	}
	delete(s.infos, key)
	delete(s.mounts, key)
	info.Kind = snapshots.KindCommitted
	info.Name = name
	for _, opt := range opts {
		if err := opt(&info); err != nil {
			return err
		}
	}
	s.infos[name] = info
	s.mounts[name] = []mount.Mount{{Type: "bind", Source: "/overlay/committed/fs"}}
	return nil
}

func (s *fakeSnapshotter) Remove(_ context.Context, key string) error {
	if _, ok := s.infos[key]; !ok {
		return errdefs.ErrNotFound
	}
	delete(s.infos, key)
	delete(s.mounts, key)
	return nil
}

func (s *fakeSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, _ ...string) error {
	for _, info := range s.infos {
		if err := fn(ctx, info); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeSnapshotter) Close() error {
	return nil
}

func TestStockImageUsesOverlay(t *testing.T) {
	delegate := newFakeSnapshotter()
	store := &fakeNodeStore{path: "/liquid/tree"}
	underTest := New(
		func(context.Context) (snapshots.Snapshotter, error) { return delegate, nil },
		fixedResolver{},
		store,
	)

	mounts, err := underTest.Prepare(t.Context(), "stock", "", snapshots.WithLabels(map[string]string{
		containersnapshots.TargetManifestDigestLabel: "sha256:manifest",
	}))
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Equal(t, "bind", mounts[0].Type)
	assert.Zero(t, store.mounts)
}

func TestLiquidImageUsesNodeStoreAndWritableOverlay(t *testing.T) {
	delegate := newFakeSnapshotter()
	store := &fakeNodeStore{path: "/liquid/tree"}
	underTest := New(
		func(context.Context) (snapshots.Snapshotter, error) { return delegate, nil },
		fixedResolver{
			found: true,
			contract: imageContract{
				Tree:      "blake3:tree",
				StoreURL:  "http://store/sync",
				HasLayers: true,
			},
		},
		store,
	)

	_, err := underTest.Prepare(t.Context(), "extract", "", snapshots.WithLabels(map[string]string{
		containersnapshots.TargetManifestDigestLabel: "sha256:manifest",
		targetSnapshotRefLabel:                       "sha256:chain",
	}))
	require.Error(t, err)
	assert.True(t, errdefs.IsAlreadyExists(err))
	assert.Equal(t, 1, store.mounts)
	assert.Equal(t, "blake3:tree", store.lastTree)
	assert.Equal(t, "http://store/sync", store.lastSource)

	committedMounts, err := underTest.Mounts(t.Context(), "sha256:chain")
	require.NoError(t, err)
	assert.Equal(t, liquidBindMount("/liquid/tree"), committedMounts)

	runtimeMounts, err := underTest.Prepare(t.Context(), "container", "sha256:chain")
	require.NoError(t, err)
	require.Len(t, runtimeMounts, 1)
	assert.Equal(t, "overlay", runtimeMounts[0].Type)
	assert.True(t, hasOption(runtimeMounts[0], "lowerdir=/liquid/tree:/overlay/lower"))
	assert.Equal(t, 1, store.mounts)
}

func TestLiquidImageFallsBackToLayers(t *testing.T) {
	delegate := newFakeSnapshotter()
	store := &fakeNodeStore{mountErr: errors.New("store unreachable")}
	underTest := New(
		func(context.Context) (snapshots.Snapshotter, error) { return delegate, nil },
		fixedResolver{
			found: true,
			contract: imageContract{
				Tree:      "blake3:tree",
				StoreURL:  "http://store/sync",
				HasLayers: true,
			},
		},
		store,
	)

	mounts, err := underTest.Prepare(t.Context(), "fallback", "", snapshots.WithLabels(map[string]string{
		containersnapshots.TargetManifestDigestLabel: "sha256:manifest",
		targetSnapshotRefLabel:                       "sha256:chain",
	}))
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Equal(t, "bind", mounts[0].Type)
	_, err = delegate.Stat(t.Context(), "fallback")
	assert.NoError(t, err)
}

func TestLiquidImageDoesNotReplaceExistingOverlaySnapshot(t *testing.T) {
	delegate := newFakeSnapshotter()
	delegate.infos["sha256:chain"] = snapshots.Info{
		Kind: snapshots.KindCommitted,
		Name: "sha256:chain",
	}
	store := &fakeNodeStore{path: "/liquid/tree"}
	underTest := New(
		func(context.Context) (snapshots.Snapshotter, error) { return delegate, nil },
		fixedResolver{
			found: true,
			contract: imageContract{
				Tree:      "blake3:tree",
				StoreURL:  "http://store/sync",
				HasLayers: true,
			},
		},
		store,
	)

	_, err := underTest.Prepare(t.Context(), "extract", "", snapshots.WithLabels(map[string]string{
		containersnapshots.TargetManifestDigestLabel: "sha256:manifest",
		targetSnapshotRefLabel:                       "sha256:chain",
	}))
	require.Error(t, err)
	assert.True(t, errdefs.IsAlreadyExists(err))
	assert.Zero(t, store.mounts)
	assert.Empty(t, delegate.infos["sha256:chain"].Labels[liquidTreeLabel])
}

func TestLiquidImageWithoutLayersFailsWhenStoreIsUnavailable(t *testing.T) {
	delegate := newFakeSnapshotter()
	store := &fakeNodeStore{mountErr: errors.New("store unreachable")}
	underTest := New(
		func(context.Context) (snapshots.Snapshotter, error) { return delegate, nil },
		fixedResolver{
			found: true,
			contract: imageContract{
				Tree:     "blake3:tree",
				StoreURL: "http://store/sync",
			},
		},
		store,
	)

	_, err := underTest.Prepare(t.Context(), "failed", "", snapshots.WithLabels(map[string]string{
		containersnapshots.TargetManifestDigestLabel: "sha256:manifest",
		targetSnapshotRefLabel:                       "sha256:chain",
	}))
	require.Error(t, err)
	assert.True(t, errdefs.IsUnavailable(err))
	assert.ErrorContains(t, err, "blake3:tree")
}

func hasOption(mount mount.Mount, expected string) bool {
	return assert.ObjectsAreEqual(expected, firstOptionWithPrefix(mount.Options, strings.SplitN(expected, "=", 2)[0]+"="))
}

func firstOptionWithPrefix(options []string, prefix string) string {
	for _, option := range options {
		if strings.HasPrefix(option, prefix) {
			return option
		}
	}
	return ""
}
