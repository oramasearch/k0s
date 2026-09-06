// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"encoding/base64"
	"testing"

	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	policy "github.com/k0sproject/k0s/pkg/liquidadmission"
	"github.com/k0sproject/k0s/static"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/k0sproject/k0s/pkg/applier"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestLiquidAdmissionConfigMapRendering(t *testing.T) {
	sets := map[string]k0sv1beta1.LiquidAdmission{
		"default": {
			Enabled:          true,
			Mode:             k0sv1beta1.LiquidAdmissionModeAttestation,
			FailurePolicy:    k0sv1beta1.LiquidAdmissionFailurePolicyFail,
			AttestationStore: "https://store.example",
		},
	}
	routes := policy.Routes{Version: 1, Default: "default", Namespaces: map[string]string{"audit": "audit--only"}}
	configMap, err := policyConfigMap(sets, routes, false)
	require.NoError(t, err)
	assert.Contains(t, configMap.Data["default.json"], `"attestation_source":"https://store.example"`)
	assert.JSONEq(t, `{"v":1,"default":"default","namespaces":{"audit":"audit--only"}}`, configMap.Data["routes.json"])
}

func TestLiquidAdmissionConfigMapPrefersNodeStore(t *testing.T) {
	sets := map[string]k0sv1beta1.LiquidAdmission{
		"default": {
			Enabled:          true,
			Mode:             k0sv1beta1.LiquidAdmissionModeAttestation,
			FailurePolicy:    k0sv1beta1.LiquidAdmissionFailurePolicyFail,
			AttestationStore: "https://store.example",
		},
	}
	routes := policy.Routes{Version: 1, Default: "default", Namespaces: map[string]string{}}
	configMap, err := policyConfigMap(sets, routes, true)
	require.NoError(t, err)
	assert.Contains(t, configMap.Data["default.json"], `"attestation_source":"http://127.0.0.1:8082"`)
}

func TestLiquidAdmissionWebhookRendering(t *testing.T) {
	resources, err := applier.ReadUnstructuredDir(static.LiquidAdmissionManifests, ".")
	require.NoError(t, err)
	ca := []byte("test-ca")
	require.NoError(t, setWebhookCA(resources, ca))
	for _, resource := range resources {
		if resource.GetKind() != "ValidatingWebhookConfiguration" {
			continue
		}
		webhooks, found, err := unstructured.NestedSlice(resource.Object, "webhooks")
		assert.NoError(t, err)
		assert.True(t, found)
		webhook := webhooks[0].(map[string]any)
		clientConfig := webhook["clientConfig"].(map[string]any)
		assert.Equal(t, base64.StdEncoding.EncodeToString(ca), clientConfig["caBundle"])
		return
	}
	t.Fatal("ValidatingWebhookConfiguration not found")
}
