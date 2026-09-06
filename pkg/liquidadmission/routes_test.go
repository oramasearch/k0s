// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidadmission

import (
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePolicies(t *testing.T) {
	defaultPolicy := v1beta1.LiquidAdmission{
		Enabled:          true,
		Mode:             v1beta1.LiquidAdmissionModeAttestation,
		FailurePolicy:    v1beta1.LiquidAdmissionFailurePolicyFail,
		AttestationStore: "https://store.example",
	}
	sets, routes, err := Resolve(defaultPolicy, []NamespacePolicy{{
		Namespace: "audit",
		Name:      "audit-only",
		Spec: v1beta1.LiquidAdmission{
			Enabled:       true,
			Mode:          v1beta1.LiquidAdmissionModeOff,
			FailurePolicy: v1beta1.LiquidAdmissionFailurePolicyFail,
		},
	}})
	require.NoError(t, err)
	assert.Equal(t, "default", routes.Default)
	assert.Equal(t, "audit--audit-only", routes.Namespaces["audit"])
	assert.Contains(t, sets, "default")
	assert.Equal(t, v1beta1.LiquidAdmissionModeOff, sets["audit--audit-only"].Mode)
}

func TestResolveRejectsMultiplePoliciesForNamespace(t *testing.T) {
	defaultPolicy := v1beta1.LiquidAdmission{
		Mode:          v1beta1.LiquidAdmissionModeOff,
		FailurePolicy: v1beta1.LiquidAdmissionFailurePolicyFail,
	}
	_, _, err := Resolve(defaultPolicy, []NamespacePolicy{
		{Namespace: "tenant", Name: "first", Spec: defaultPolicy},
		{Namespace: "tenant", Name: "second", Spec: defaultPolicy},
	})
	require.ErrorContains(t, err, "multiple admission policies")
}

func TestPolicyJSONUsesLiquidFields(t *testing.T) {
	policy := v1beta1.LiquidAdmission{
		Mode:             v1beta1.LiquidAdmissionModeAttestation,
		Issuers:          []string{"taap:1:ed25519:issuer"},
		Profiles:         []string{"image_provenance/1"},
		AttestationStore: "https://store.example",
	}
	encoded, err := PolicyJSON(policy)
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":1,"mode":"attestation","issuers":["taap:1:ed25519:issuer"],"profiles":["image_provenance/1"],"exempt":[],"exempt_namespaces":["kube-system"],"attestation_source":"https://store.example","log":{"lake":"","max_checkpoint_age_seconds":0}}`, string(encoded))
}
