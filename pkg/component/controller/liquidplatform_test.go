// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiquidPlatformReconcile(t *testing.T) {
	manifestsDir := t.TempDir()
	component := NewLiquidPlatform(manifestsDir)
	require.NoError(t, component.Init(t.Context()))

	config := v1beta1.DefaultClusterConfig()
	config.Spec.Liquid.Platform.ACMEDirectory = "https://lake.example/acme/customer/directory"
	require.NoError(t, component.Reconcile(t.Context(), config))

	manifest, err := os.ReadFile(filepath.Join(manifestsDir, "liquid-platform", "platform.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "kind: GatewayClass")
	assert.Contains(t, string(manifest), "kind: ClusterIssuer")
	assert.Contains(t, string(manifest), "https://lake.example/acme/customer/directory")
	assert.Contains(t, string(manifest), "kind: ValidatingAdmissionPolicy")
}

func TestLiquidPlatformReconcileRemovesManifestWithoutLiquid(t *testing.T) {
	manifestsDir := t.TempDir()
	component := NewLiquidPlatform(manifestsDir)
	require.NoError(t, component.Init(t.Context()))
	require.NoError(t, component.Reconcile(t.Context(), v1beta1.DefaultClusterConfig()))

	config := v1beta1.DefaultClusterConfig()
	config.Spec.Liquid = nil
	require.NoError(t, component.Reconcile(t.Context(), config))

	_, err := os.Stat(filepath.Join(manifestsDir, "liquid-platform"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
