// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeimage

import (
	"sort"

	liquidv1alpha1 "github.com/k0sproject/k0s/pkg/apis/liquid/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	RoleController = "controller"
	RoleWorker     = "worker"
)

// BuildTargets resolves and orders a NodeImage plan's immutable target list.
func BuildTargets(nodes []corev1.Node, selector metav1.LabelSelector) ([]liquidv1alpha1.NodeImageTargetStatus, error) {
	compiled, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		return nil, err
	}
	targets := make([]liquidv1alpha1.NodeImageTargetStatus, 0, len(nodes))
	for _, node := range nodes {
		if !compiled.Matches(labels.Set(node.Labels)) {
			continue
		}
		targets = append(targets, liquidv1alpha1.NodeImageTargetStatus{
			Name:  node.Name,
			Role:  nodeRole(node),
			State: liquidv1alpha1.NodeImageTargetPending,
		})
	}
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].Role != targets[right].Role {
			return targets[left].Role == RoleController
		}
		return targets[left].Name < targets[right].Name
	})
	return targets, nil
}

// ActiveTarget returns the only target that may currently receive an instruction.
func ActiveTarget(targets []liquidv1alpha1.NodeImageTargetStatus) int {
	for index := range targets {
		switch targets[index].State {
		case liquidv1alpha1.NodeImageTargetHealthy:
			continue
		case liquidv1alpha1.NodeImageTargetRolledBack, liquidv1alpha1.NodeImageTargetFailed:
			return -1
		default:
			return index
		}
	}
	return -1
}

// PlanPhase derives the plan phase from its target states.
func PlanPhase(targets []liquidv1alpha1.NodeImageTargetStatus) string {
	if len(targets) == 0 {
		return liquidv1alpha1.NodeImagePhaseCompleted
	}
	allHealthy := true
	for _, target := range targets {
		switch target.State {
		case liquidv1alpha1.NodeImageTargetRolledBack, liquidv1alpha1.NodeImageTargetFailed:
			return liquidv1alpha1.NodeImagePhaseFailed
		case liquidv1alpha1.NodeImageTargetHealthy:
		default:
			allHealthy = false
		}
	}
	if allHealthy {
		return liquidv1alpha1.NodeImagePhaseCompleted
	}
	return liquidv1alpha1.NodeImagePhaseRunning
}

func nodeRole(node corev1.Node) string {
	if _, found := node.Labels["node-role.kubernetes.io/control-plane"]; found {
		return RoleController
	}
	if _, found := node.Labels["node-role.kubernetes.io/master"]; found {
		return RoleController
	}
	return RoleWorker
}
