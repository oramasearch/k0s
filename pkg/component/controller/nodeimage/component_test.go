// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeimage

import (
	"encoding/json"
	"testing"
	"time"

	liquidv1alpha1 "github.com/k0sproject/k0s/pkg/apis/liquid/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestReadinessDeadlineRequestsAndRecordsRollback(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	started := metav1.NewTime(now.Add(-time.Minute))
	rollback := true
	image := &liquidv1alpha1.NodeImage{
		ObjectMeta: metav1.ObjectMeta{Name: "upgrade-1"},
		Spec: liquidv1alpha1.NodeImageSpec{
			Strategy: liquidv1alpha1.NodeImageStrategy{
				Deadline:          metav1.Duration{Duration: 30 * time.Second},
				AutomaticRollback: &rollback,
			},
		},
		Status: liquidv1alpha1.NodeImageStatus{ObservedDigest: "registry.example/node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	target := &liquidv1alpha1.NodeImageTargetStatus{
		Name:      "worker-a",
		Role:      RoleWorker,
		State:     liquidv1alpha1.NodeImageTargetRebooting,
		StartedAt: &started,
	}
	node := readyNode("worker-a")
	node.Status.Conditions[0].Status = corev1.ConditionFalse
	client := fake.NewSimpleClientset(node)

	changed, err := advanceTarget(t.Context(), client.CoreV1().Nodes(), image, target, node, now)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, liquidv1alpha1.NodeImageTargetRollingBack, target.State)
	assert.Equal(t, now, target.StartedAt.Time)

	updated, err := client.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	require.NoError(t, err)
	var instruction ImageInstruction
	require.NoError(t, json.Unmarshal([]byte(updated.Annotations[NodeImageInstructionAnnotation]), &instruction))
	assert.Equal(t, "rollback", instruction.Action)

	updated.Annotations[NodeImageStateAnnotation] = "rolled-back:upgrade-1"
	updated.Status.Conditions[0].Status = corev1.ConditionTrue
	changed, err = advanceTarget(t.Context(), client.CoreV1().Nodes(), image, target, updated, now.Add(time.Second))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, liquidv1alpha1.NodeImageTargetRolledBack, target.State)
}

func TestNodeAgentRBACIsLimitedToOneNode(t *testing.T) {
	client := fake.NewSimpleClientset()

	require.NoError(t, ensureNodeAgentRBAC(t.Context(), client, "worker-a"))
	roles, err := client.RbacV1().ClusterRoles().List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roles.Items, 1)
	require.Len(t, roles.Items[0].Rules, 1)
	assert.Equal(t, []string{"worker-a"}, roles.Items[0].Rules[0].ResourceNames)
	assert.NotContains(t, roles.Items[0].Rules[0].Verbs, "list")

	bindings, err := client.RbacV1().ClusterRoleBindings().List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, bindings.Items, 1)
	require.Len(t, bindings.Items[0].Subjects, 1)
	assert.Equal(t, "system:node:worker-a", bindings.Items[0].Subjects[0].Name)
}
