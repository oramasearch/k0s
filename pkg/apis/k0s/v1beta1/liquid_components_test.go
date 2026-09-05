// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultLiquidComponents(t *testing.T) {
	config := DefaultClusterConfig()
	require.NotNil(t, config.Spec.Liquid)
	require.NotNil(t, config.Spec.Liquid.Components)
	assert.Equal(t, "custom", config.Spec.Network.Provider)

	entries := config.Spec.Liquid.Components.Entries()
	require.Len(t, entries, 4)
	for _, entry := range entries {
		assert.True(t, entry.Component.Enabled, entry.Name)
		assert.NotEmpty(t, entry.Component.Version, entry.Name)
		assert.Equal(t, LiquidReplacementManaged, entry.Component.Replacement, entry.Name)
	}
	assert.NoError(t, errors.Join(config.Validate()...))
}

func TestExistingConfigDoesNotAcquireLiquidDefaults(t *testing.T) {
	config, err := ConfigFromBytes([]byte(`
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
spec:
  network:
    provider: kuberouter
`))
	require.NoError(t, err)
	assert.Nil(t, config.Spec.Liquid)
	assert.Equal(t, "kuberouter", config.Spec.Network.Provider)
}

func TestLiquidCiliumDefaultsNetworkProvider(t *testing.T) {
	config, err := ConfigFromBytes([]byte(`
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
spec:
  liquid:
    components:
      cilium:
        enabled: true
        version: 1.20.1
        chart:
          repository: https://helm.cilium.io
          name: cilium
        replacement: managed
      gatewayAPI:
        enabled: false
        version: 1.6.0
        chart:
          repository: https://wiremind.github.io/wiremind-helm-charts
          name: gateway-api-crds
        replacement: managed
      localPath:
        enabled: false
        version: 0.0.38
        chart:
          repository: https://charts.containeroo.ch
          name: local-path-provisioner
        replacement: managed
      distributedStorage:
        enabled: false
        version: 1.12.1
        chart:
          repository: https://charts.longhorn.io
          name: longhorn
        replacement: managed
`))
	require.NoError(t, err)
	assert.Equal(t, "custom", config.Spec.Network.Provider)
	assert.NoError(t, errors.Join(config.Validate()...))
}

func TestLiquidCiliumRejectsBuiltInNetworkProvider(t *testing.T) {
	for _, provider := range []string{"kuberouter", "calico"} {
		t.Run(provider, func(t *testing.T) {
			config := DefaultClusterConfig()
			config.Spec.Network.Provider = provider

			err := errors.Join(config.Validate()...)
			require.Error(t, err)
			assert.ErrorContains(t, err, "spec.liquid.components.cilium.enabled")
			assert.ErrorContains(t, err, "spec.network.provider")
		})
	}
}

func TestLiquidExternalReplacementIsValid(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Components.DistributedStorage.Replacement = LiquidReplacementExternal
	assert.NoError(t, errors.Join(config.Validate()...))
}

func TestLiquidComponentValidation(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Components.LocalPath.Version = ""
	config.Spec.Liquid.Components.GatewayAPI.Chart.Repository = "http://example.com"
	config.Spec.Liquid.Components.DistributedStorage.Replacement = "other"

	err := errors.Join(config.Validate()...)
	require.Error(t, err)
	assert.ErrorContains(t, err, "localPath.version")
	assert.ErrorContains(t, err, "gatewayAPI.chart.repository")
	assert.ErrorContains(t, err, "distributedStorage.replacement")
}
