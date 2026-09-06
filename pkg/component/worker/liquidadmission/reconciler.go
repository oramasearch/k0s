// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

// Package liquidadmission manages the node-local static admission Pod.
package liquidadmission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/component/worker"
	"github.com/k0sproject/k0s/pkg/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	Name              = "liquid-admission"
	Namespace         = metav1.NamespaceSystem
	PolicyConfigMap   = "liquid-admission-policies"
	TLSSecret         = "liquid-admission-tls"
	LiquidListen      = "127.0.0.1:8443"
	AdapterListen     = ":9443"
	LiquidControlPort = "127.0.0.1:8083"
)

// Reconciler exposes liquid and the k0s adapter as one static Pod per worker.
type Reconciler struct {
	vars       *config.CfgVars
	staticPods worker.StaticPods
	pauseImage *v1beta1.ImageSpec
	pod        worker.StaticPod
}

var _ manager.Component = (*Reconciler)(nil)

// NewReconciler creates the static admission Pod reconciler.
func NewReconciler(vars *config.CfgVars, staticPods worker.StaticPods, pauseImage *v1beta1.ImageSpec) *Reconciler {
	return &Reconciler{vars: vars, staticPods: staticPods, pauseImage: pauseImage.DeepCopy()}
}

// Init validates the static Pod inputs.
func (r *Reconciler) Init(context.Context) error {
	if r.pauseImage == nil {
		return errors.New("liquid admission static Pod requires a pause image")
	}
	return nil
}

// Start publishes the static Pod to the local kubelet.
func (r *Reconciler) Start(context.Context) error {
	pod, err := r.staticPods.ClaimStaticPod(Namespace, Name)
	if err != nil {
		return fmt.Errorf("claim liquid admission static Pod: %w", err)
	}
	r.pod = pod

	manifest, err := r.manifest()
	if err != nil {
		r.pod.Drop()
		r.pod = nil
		return err
	}
	if err := r.pod.SetManifest(manifest); err != nil {
		r.pod.Drop()
		r.pod = nil
		return fmt.Errorf("set liquid admission static Pod manifest: %w", err)
	}
	return nil
}

// Stop removes the static Pod from the local kubelet.
func (r *Reconciler) Stop() error {
	if r.pod != nil {
		r.pod.Drop()
		r.pod = nil
	}
	return nil
}

func (r *Reconciler) manifest() (*corev1.Pod, error) {
	k0sBinary, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find k0s executable: %w", err)
	}

	readOnly := true
	noPrivilegeEscalation := false
	priority := int32(2000001000)
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: Namespace,
			Labels:    applier.CommonLabels(Name),
		},
		Spec: corev1.PodSpec{
			HostNetwork:        true,
			ServiceAccountName: Name,
			PriorityClassName:  "system-node-critical",
			Priority:           &priority,
			Tolerations: []corev1.Toleration{{
				Operator: corev1.TolerationOpExists,
			}},
			Containers: []corev1.Container{
				{
					Name:            "liquid",
					Image:           r.pauseImage.URI(),
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"/usr/local/bin/liquid"},
					Args: []string{
						"serve",
						"admission",
						"--policies",
						"/etc/liquid/policies",
						"--listen",
						LiquidListen,
						"--control-listen",
						LiquidControlPort,
						"--tls-cert",
						"/etc/liquid/tls/tls.crt",
						"--tls-key",
						"/etc/liquid/tls/tls.key",
						"--log-cache",
						"/var/lib/liquid-admission/checkpoint",
					},
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem:   &readOnly,
						AllowPrivilegeEscalation: &noPrivilegeEscalation,
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "liquid-binary", MountPath: "/usr/local/bin/liquid", ReadOnly: true},
						{Name: "policies", MountPath: "/etc/liquid/policies", ReadOnly: true},
						{Name: "tls", MountPath: "/etc/liquid/tls", ReadOnly: true},
						{Name: "state", MountPath: "/var/lib/liquid-admission"},
					},
				},
				{
					Name:            "adapter",
					Image:           r.pauseImage.URI(),
					ImagePullPolicy: corev1.PullIfNotPresent,
					Ports: []corev1.ContainerPort{{
						Name:          "https",
						ContainerPort: 9443,
						Protocol:      corev1.ProtocolTCP,
					}},
					Command: []string{"/usr/local/bin/k0s"},
					Args: []string{
						"liquid",
						"admission-adapter",
						"--listen",
						AdapterListen,
						"--liquid",
						"https://" + LiquidListen,
						"--control",
						"https://" + LiquidControlPort,
						"--routes",
						"/etc/liquid/policies/routes.json",
						"--tls-cert",
						"/etc/liquid/tls/tls.crt",
						"--tls-key",
						"/etc/liquid/tls/tls.key",
						"--tls-ca",
						"/etc/liquid/tls/ca.crt",
						"--tls-server-name",
						"liquid-admission.kube-system.svc",
					},
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem:   &readOnly,
						AllowPrivilegeEscalation: &noPrivilegeEscalation,
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "k0s-binary", MountPath: "/usr/local/bin/k0s", ReadOnly: true},
						{Name: "policies", MountPath: "/etc/liquid/policies", ReadOnly: true},
						{Name: "tls", MountPath: "/etc/liquid/tls", ReadOnly: true},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "liquid-binary",
					VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
						Path: filepath.Join(r.vars.BinDir, "liquid"),
						Type: ptr.To(corev1.HostPathFile),
					}},
				},
				{
					Name: "k0s-binary",
					VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
						Path: k0sBinary,
						Type: ptr.To(corev1.HostPathFile),
					}},
				},
				{
					Name: "policies",
					VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: PolicyConfigMap},
					}},
				},
				{
					Name: "tls",
					VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
						SecretName: TLSSecret,
					}},
				},
				{Name: "state", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}, nil
}
