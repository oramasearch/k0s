// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"context"
	"testing"

	"github.com/k0sproject/k0s/pkg/liquidadmission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestEventSinkWritesDeniedAdmissionEvent(t *testing.T) {
	kube := kubefake.NewSimpleClientset()
	sink := eventSink{kube: kube}
	decision := liquidadmission.Decision{
		Image:   "registry.example/app@sha256:deadbeef",
		Outcome: "denied",
		Reasons: []struct {
			Message string `json:"message"`
		}{{Message: "no attestation"}},
	}

	require.NoError(t, sink.Emit(context.Background(), "tenant-a", decision))
	events, err := kube.CoreV1().Events("tenant-a").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, events.Items, 1)
	assert.Equal(t, corev1.EventTypeWarning, events.Items[0].Type)
	assert.Equal(t, "LiquidAdmissionDenied", events.Items[0].Reason)
	assert.Equal(t, "registry.example/app@sha256:deadbeef: no attestation", events.Items[0].Message)
}
