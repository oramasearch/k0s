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
	require.NotNil(t, config.Spec.Liquid.NodeStore)
	assert.True(t, config.Spec.Liquid.NodeStore.Enabled)
	require.NotNil(t, config.Spec.Liquid.Platform)
	assert.Equal(t, "http://loki.liquid-system.svc.cluster.local:3100", config.Spec.Liquid.Platform.LogsEndpoint)
	require.NotNil(t, config.Spec.Liquid.Admission)
	assert.True(t, config.Spec.Liquid.Admission.Enabled)
	assert.Equal(t, LiquidAdmissionModeOff, config.Spec.Liquid.Admission.Mode)
	assert.Equal(t, LiquidAdmissionFailurePolicyFail, config.Spec.Liquid.Admission.FailurePolicy)
	assert.Equal(t, "custom", config.Spec.Network.Provider)

	entries := config.Spec.Liquid.Components.Entries()
	require.Len(t, entries, 6)
	for _, entry := range entries {
		assert.True(t, entry.Component.Enabled, entry.Name)
		assert.NotEmpty(t, entry.Component.Version, entry.Name)
		assert.Equal(t, LiquidReplacementManaged, entry.Component.Replacement, entry.Name)
	}
	assert.NoError(t, errors.Join(config.Validate()...))
}

func TestLiquidPlatformValidation(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Platform.ACMEDirectory = "ftp://lake.example/acme/directory"
	config.Spec.Liquid.Platform.LogsEndpoint = "https://logs.example/path?token=secret"

	err := errors.Join(config.Validate()...)
	require.Error(t, err)
	assert.ErrorContains(t, err, "platform.acmeDirectory")
	assert.ErrorContains(t, err, "platform.logsEndpoint")
}

func TestLiquidLogShipperValuesUseConfiguredEndpoint(t *testing.T) {
	liquid := DefaultLiquidSpec()
	liquid.Platform.LogsEndpoint = "https://logs.example/loki"

	values := liquid.LiquidComponentValues(LiquidComponentLogShipper)
	assert.Contains(t, values, `endpoint: "https://logs.example/loki"`)
	assert.NotContains(t, values, "${LIQUID_LOGS_ENDPOINT}")
	assert.Contains(t, values, "kubernetes.pod_namespace")
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
      certManager:
        enabled: false
        version: v1.21.1
        chart:
          repository: https://charts.jetstack.io
          name: cert-manager
        replacement: managed
      logShipper:
        enabled: false
        version: 0.58.0
        chart:
          repository: https://helm.vector.dev
          name: vector
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
    platform:
      logsEndpoint: http://loki.liquid-system.svc.cluster.local:3100
`))
	require.NoError(t, err)
	assert.Nil(t, config.Spec.Liquid.NodeStore)
	assert.Nil(t, config.Spec.Liquid.Admission)
	assert.Equal(t, "custom", config.Spec.Network.Provider)
	assert.NoError(t, errors.Join(config.Validate()...))
}

func TestLiquidAdmissionValidation(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Admission.Mode = "invalid"
	config.Spec.Liquid.Admission.FailurePolicy = "Ignore"

	err := errors.Join(config.Validate()...)
	require.Error(t, err)
	assert.ErrorContains(t, err, "admission.mode")
	assert.ErrorContains(t, err, "admission.failurePolicy")
}

func TestLiquidAdmissionRequiresStoreWithoutNodeStore(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Admission.Mode = LiquidAdmissionModeAttestation
	config.Spec.Liquid.NodeStore.Enabled = false

	err := errors.Join(config.Validate()...)
	require.Error(t, err)
	assert.ErrorContains(t, err, "admission.attestationStore")

	config.Spec.Liquid.Admission.AttestationStore = "https://attestations.example"
	assert.NoError(t, errors.Join(config.Validate()...))
}

func TestLiquidAdmissionLogValidation(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Admission.Mode = LiquidAdmissionModeAttestationLog
	config.Spec.Liquid.Admission.Log = &LiquidAdmissionLog{
		Mode: LiquidAdmissionLogModeVerify,
		Lake: "https://lake.example",
		Trust: LiquidAdmissionLogTrust{
			Identity: LiquidAdmissionLogIdentity{
				TAAPID:    "taap:1:ed25519:lake",
				PublicKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
			WitnessThreshold: 1,
			Witnesses: []LiquidAdmissionLogWitness{{
				Endpoint:  "https://witness.example/checkpoint",
				PublicKey: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			}},
		},
		MaximumCheckpointAgeSeconds: 300,
	}

	assert.NoError(t, errors.Join(config.Validate()...))

	config.Spec.Liquid.Admission.Log.Trust.Identity.PublicKey = "not-a-key"
	config.Spec.Liquid.Admission.Log.Trust.WitnessThreshold = 2
	config.Spec.Liquid.Admission.Log.Trust.Witnesses[0].Endpoint = "ftp://witness.example"

	err := errors.Join(config.Validate()...)
	require.Error(t, err)
	assert.ErrorContains(t, err, "admission.log.trust.identity.publicKey")
	assert.ErrorContains(t, err, "admission.log.trust.witnessThreshold")
	assert.ErrorContains(t, err, "admission.log.trust.witnesses[0].endpoint")
}

func TestLiquidAdmissionDenyLogRequiresNoTrust(t *testing.T) {
	config := DefaultClusterConfig()
	config.Spec.Liquid.Admission.Log = &LiquidAdmissionLog{
		Mode: LiquidAdmissionLogModeDeny,
	}

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
