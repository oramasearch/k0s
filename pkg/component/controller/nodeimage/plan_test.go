// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeimage

import (
	"testing"

	liquidv1alpha1 "github.com/k0sproject/k0s/pkg/apis/liquid/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildTargetsOrdersControllersBeforeWorkers(t *testing.T) {
	nodes := []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", Labels: map[string]string{"pool": "liquid"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "controller-z", Labels: map[string]string{"pool": "liquid", "node-role.kubernetes.io/control-plane": "true"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "ignored", Labels: map[string]string{"pool": "other"}}},
	}

	targets, err := BuildTargets(nodes, metav1.LabelSelector{MatchLabels: map[string]string{"pool": "liquid"}})
	require.NoError(t, err)
	require.Len(t, targets, 2)
	assert.Equal(t, "controller-z", targets[0].Name)
	assert.Equal(t, RoleController, targets[0].Role)
	assert.Equal(t, "worker-a", targets[1].Name)
	assert.Equal(t, RoleWorker, targets[1].Role)
}

func TestActiveTargetAllowsOnlyOneAtATime(t *testing.T) {
	targets := []liquidv1alpha1.NodeImageTargetStatus{
		{State: liquidv1alpha1.NodeImageTargetHealthy},
		{State: liquidv1alpha1.NodeImageTargetRebooting},
		{State: liquidv1alpha1.NodeImageTargetPending},
	}
	assert.Equal(t, 1, ActiveTarget(targets))

	targets[1].State = liquidv1alpha1.NodeImageTargetHealthy
	assert.Equal(t, 2, ActiveTarget(targets))

	targets[2].State = liquidv1alpha1.NodeImageTargetRolledBack
	assert.Equal(t, -1, ActiveTarget(targets))
	assert.Equal(t, liquidv1alpha1.NodeImagePhaseFailed, PlanPhase(targets))
}
