// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"strings"
	"testing"

	liquidv1alpha1 "github.com/k0sproject/k0s/pkg/apis/liquid/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrintNodeImageStatus(t *testing.T) {
	plans := []liquidv1alpha1.NodeImage{{
		ObjectMeta: metav1.ObjectMeta{Name: "build-42"},
		Status: liquidv1alpha1.NodeImageStatus{
			ObservedDigest: "registry.example/node@sha256:abc",
			Phase:          liquidv1alpha1.NodeImagePhaseRunning,
			Targets: []liquidv1alpha1.NodeImageTargetStatus{{
				Name:  "controller-a",
				Role:  "controller",
				State: "Rebooting",
			}},
		},
	}}
	var output strings.Builder

	require.NoError(t, printNodeImageStatus(&output, plans))
	assert.Contains(t, output.String(), "build-42")
	assert.Contains(t, output.String(), "controller-a")
	assert.Contains(t, output.String(), "Rebooting")
}
