// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestNodeImageAndBreakGlassRegisterAndRoundTrip(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	automaticRollback := true
	resources := []runtime.Object{
		&NodeImage{
			TypeMeta: metav1.TypeMeta{APIVersion: SchemeGroupVersion.String(), Kind: "NodeImage"},
			Spec: NodeImageSpec{
				Digest: "registry.example/node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Strategy: NodeImageStrategy{
					Deadline:          metav1.Duration{Duration: 10 * time.Minute},
					AutomaticRollback: &automaticRollback,
				},
			},
		},
		&BreakGlass{
			TypeMeta: metav1.TypeMeta{APIVersion: SchemeGroupVersion.String(), Kind: "BreakGlass"},
			Spec: BreakGlassSpec{
				Node:       "node-a",
				Principal:  "operator@example",
				Reason:     "incident-1",
				TTLSeconds: 3600,
			},
		},
	}
	for _, resource := range resources {
		encoded, err := json.Marshal(resource)
		require.NoError(t, err)
		decoded, err := scheme.New(resource.GetObjectKind().GroupVersionKind())
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, decoded))
		assert.Equal(t, resource.GetObjectKind().GroupVersionKind(), decoded.GetObjectKind().GroupVersionKind())
	}
}
