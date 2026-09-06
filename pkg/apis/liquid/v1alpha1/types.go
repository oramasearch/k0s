// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NodeImagePhasePending   = "Pending"
	NodeImagePhaseRunning   = "Running"
	NodeImagePhaseCompleted = "Completed"
	NodeImagePhaseFailed    = "Failed"

	NodeImageTargetPending     = "Pending"
	NodeImageTargetSwitching   = "Switching"
	NodeImageTargetRebooting   = "Rebooting"
	NodeImageTargetHealthy     = "Healthy"
	NodeImageTargetRollingBack = "RollingBack"
	NodeImageTargetRolledBack  = "RolledBack"
	NodeImageTargetFailed      = "Failed"

	BreakGlassPhasePending = "Pending"
	BreakGlassPhaseOpen    = "Open"
	BreakGlassPhaseClosed  = "Closed"
	BreakGlassPhaseFailed  = "Failed"
)

// NodeImage declares an immutable bootc deployment for selected nodes.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=nimg
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Digest",type=string,JSONPath=`.spec.digest`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +genclient
// +genclient:nonNamespaced
type NodeImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec NodeImageSpec `json:"spec"`
	// +optional
	Status NodeImageStatus `json:"status,omitempty"`
}

// NodeImageSpec selects nodes and the digest-pinned bootc image to deploy.
type NodeImageSpec struct {
	// Digest is an OCI image reference pinned with an sha256 digest.
	// +kubebuilder:validation:Pattern=`^.+@sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`
	// Selector is evaluated once when the plan starts.
	Selector metav1.LabelSelector `json:"selector"`
	// +optional
	Strategy NodeImageStrategy `json:"strategy,omitempty"`
}

// NodeImageStrategy controls readiness and rollback behavior.
type NodeImageStrategy struct {
	// Deadline is the maximum time a rebooted node may remain unready.
	// +kubebuilder:default="10m"
	// +optional
	Deadline metav1.Duration `json:"deadline,omitempty"`
	// AutomaticRollback rolls a node back after the readiness deadline.
	// +kubebuilder:default=true
	// +optional
	AutomaticRollback *bool `json:"automaticRollback,omitempty"`
}

// NodeImageStatus records the immutable target list and rollout progress.
type NodeImageStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	ObservedDigest string `json:"observedDigest,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	Targets []NodeImageTargetStatus `json:"targets,omitempty"`
}

// NodeImageTargetStatus records one node's image transition.
type NodeImageTargetStatus struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	State string `json:"state"`
	// +optional
	PreviousDigest string `json:"previousDigest,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
}

// NodeImageList is a list of NodeImage resources.
//
// +kubebuilder:object:root=true
type NodeImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []NodeImage `json:"items"`
}

// BreakGlass requests temporary certificate-only SSH access to one node.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=bg
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.node`
// +kubebuilder:printcolumn:name="Principal",type=string,JSONPath=`.spec.principal`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +genclient
// +genclient:nonNamespaced
type BreakGlass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec BreakGlassSpec `json:"spec"`
	// +optional
	Status BreakGlassStatus `json:"status,omitempty"`
}

// BreakGlassSpec identifies the node, certificate principal, reason, and lifetime.
type BreakGlassSpec struct {
	Node      string `json:"node"`
	Principal string `json:"principal"`
	// +kubebuilder:validation:MinLength=1
	Reason string `json:"reason"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	TTLSeconds int32 `json:"ttlSeconds"`
}

// BreakGlassStatus records when the host opened and closed access.
type BreakGlassStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	OpenedAt *metav1.Time `json:"openedAt,omitempty"`
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// +optional
	ClosedAt *metav1.Time `json:"closedAt,omitempty"`
}

// BreakGlassList is a list of BreakGlass resources.
//
// +kubebuilder:object:root=true
type BreakGlassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []BreakGlass `json:"items"`
}
