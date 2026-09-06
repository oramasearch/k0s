// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package containerd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalLiquidDropIn(t *testing.T) {
	data, err := marshalLiquidDropIn("/run/k0s/liquid-snapshotter.sock")
	require.NoError(t, err)
	assert.Contains(t, string(data), liquidDropInHeader)

	tree, err := toml.LoadBytes(data)
	require.NoError(t, err)
	assert.Equal(t, int64(3), tree.Get("version"))
	assert.Equal(t, "snapshot", tree.GetPath([]string{"proxy_plugins", "liquid", "type"}))
	assert.Equal(t, "/run/k0s/liquid-snapshotter.sock", tree.GetPath([]string{"proxy_plugins", "liquid", "address"}))
	assert.Equal(t, "liquid", tree.GetPath([]string{"plugins", "io.containerd.cri.v1.images", "snapshotter"}))
}

func TestReconcileLiquidDropIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liquid.toml")

	require.NoError(t, reconcileLiquidDropIn(dir, "/run/k0s/liquid-snapshotter.sock"))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "snapshotter = \"liquid\"")

	require.NoError(t, reconcileLiquidDropIn(dir, ""))
	_, err = os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestReconcileLiquidDropInPreservesUserFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liquid.toml")
	require.NoError(t, os.WriteFile(path, []byte("version = 3\n"), 0644))

	require.NoError(t, reconcileLiquidDropIn(dir, ""))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "version = 3\n", string(data))
}
