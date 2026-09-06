// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/component/worker/liquidadmission"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/leaderelection"
	policy "github.com/k0sproject/k0s/pkg/liquidadmission"
	"github.com/k0sproject/k0s/static"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const LiquidAdmissionStackName = "liquid-admission"

const liquidNodeStoreAttestationSource = "http://127.0.0.1:8082"

var admissionPolicyGVR = schema.GroupVersionResource{
	Group: "liquid.k0sproject.io", Version: "v1alpha1", Resource: "admissionpolicies",
}

// LiquidAdmission reconciles the policy files and webhook objects for liquid admission.
type LiquidAdmission struct {
	clients       kubernetes.ClientFactoryInterface
	leaderElector leaderelector.Interface
	vars          *config.CfgVars

	mu               sync.RWMutex
	config           *k0sv1beta1.LiquidAdmission
	nodeStoreEnabled bool
	stop             func()
}

var _ manager.Component = (*LiquidAdmission)(nil)
var _ manager.Reconciler = (*LiquidAdmission)(nil)

func NewLiquidAdmission(clients kubernetes.ClientFactoryInterface, leaderElector leaderelector.Interface, vars *config.CfgVars) *LiquidAdmission {
	return &LiquidAdmission{clients: clients, leaderElector: leaderElector, vars: vars}
}

func (*LiquidAdmission) Init(context.Context) error { return nil }

func (a *LiquidAdmission) Reconcile(_ context.Context, cluster *k0sv1beta1.ClusterConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cluster == nil || cluster.Spec == nil || cluster.Spec.Liquid == nil {
		a.config = nil
		a.nodeStoreEnabled = false
		return nil
	}
	a.config = cluster.Spec.Liquid.Admission.DeepCopy()
	a.nodeStoreEnabled = cluster.Spec.Liquid.NodeStore != nil && cluster.Spec.Liquid.NodeStore.Enabled
	return nil
}

func (a *LiquidAdmission) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		leaderelection.RunLeaderTasks(ctx, a.leaderElector.CurrentStatus, func(ctx context.Context) {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				if err := a.apply(ctx); err != nil && ctx.Err() == nil {
					continue
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					return
				}
			}
		})
	}()
	a.stop = func() { cancel(); <-done }
	return nil
}

func (a *LiquidAdmission) Stop() error {
	if a.stop != nil {
		a.stop()
	}
	return nil
}

func (a *LiquidAdmission) apply(ctx context.Context) error {
	defaultPolicy, nodeStoreEnabled := a.defaultPolicy()
	if defaultPolicy == nil || !defaultPolicy.Enabled {
		return nil
	}
	dynamicClient, err := a.clients.GetDynamicClient()
	if err != nil {
		return err
	}
	resources, err := dynamicClient.Resource(admissionPolicyGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list admission policies: %w", err)
	}
	policies, err := decodeAdmissionPolicies(resources.Items, nodeStoreEnabled)
	if err != nil {
		return err
	}
	sets, routes, err := policy.Resolve(*defaultPolicy, policies)
	if err != nil {
		return err
	}
	stackResources, err := applier.ReadUnstructuredDir(static.LiquidAdmissionManifests, ".")
	if err != nil {
		return err
	}
	configMap, err := policyConfigMap(sets, routes, nodeStoreEnabled)
	if err != nil {
		return err
	}
	secret, ca, err := a.tlsSecret()
	if err != nil {
		return err
	}
	stackResources = append(stackResources, toUnstructured(configMap), toUnstructured(secret))
	if err := setWebhookCA(stackResources, ca); err != nil {
		return err
	}
	return (&applier.Stack{Name: LiquidAdmissionStackName, Resources: stackResources, Clients: a.clients}).Apply(ctx, true)
}

func (a *LiquidAdmission) defaultPolicy() (*k0sv1beta1.LiquidAdmission, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.DeepCopy(), a.nodeStoreEnabled
}

func decodeAdmissionPolicies(items []unstructured.Unstructured, nodeStoreEnabled bool) ([]policy.NamespacePolicy, error) {
	policies := make([]policy.NamespacePolicy, 0, len(items))
	for _, item := range items {
		spec, found, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil || !found {
			return nil, fmt.Errorf("read admission policy %s/%s", item.GetNamespace(), item.GetName())
		}
		encoded, err := json.Marshal(spec)
		if err != nil {
			return nil, err
		}
		var decoded k0sv1beta1.LiquidAdmission
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return nil, fmt.Errorf("decode admission policy %s/%s: %w", item.GetNamespace(), item.GetName(), err)
		}
		decoded.Enabled = true
		if decoded.Mode != k0sv1beta1.LiquidAdmissionModeOff && decoded.AttestationStore == "" && !nodeStoreEnabled {
			return nil, fmt.Errorf("admission policy %s/%s requires an attestation store when the node store is disabled", item.GetNamespace(), item.GetName())
		}
		policies = append(policies, policy.NamespacePolicy{Namespace: item.GetNamespace(), Name: item.GetName(), Spec: decoded})
	}
	return policies, nil
}

func policyConfigMap(sets map[string]k0sv1beta1.LiquidAdmission, routes policy.Routes, nodeStoreEnabled bool) (*corev1.ConfigMap, error) {
	data := make(map[string]string, len(sets)+1)
	for name, entry := range sets {
		if nodeStoreEnabled {
			entry.AttestationStore = liquidNodeStoreAttestationSource
		}
		encoded, err := policy.PolicyJSON(entry)
		if err != nil {
			return nil, err
		}
		data[name+".json"] = string(encoded)
	}
	encodedRoutes, err := json.Marshal(routes)
	if err != nil {
		return nil, err
	}
	data["routes.json"] = string(encodedRoutes)
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: liquidadmission.PolicyConfigMap, Namespace: metav1.NamespaceSystem},
		Data:       data,
	}, nil
}

func (a *LiquidAdmission) tlsSecret() (*corev1.Secret, []byte, error) {
	certRoot := a.vars.CertRootDir
	cert, err := os.ReadFile(filepath.Join(certRoot, "liquid-admission.crt"))
	if err != nil {
		return nil, nil, err
	}
	key, err := os.ReadFile(filepath.Join(certRoot, "liquid-admission.key"))
	if err != nil {
		return nil, nil, err
	}
	ca, err := os.ReadFile(filepath.Join(certRoot, "ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: liquidadmission.TLSSecret, Namespace: metav1.NamespaceSystem},
		Data:       map[string][]byte{"tls.crt": cert, "tls.key": key, "ca.crt": ca},
	}, ca, nil
}

func setWebhookCA(resources []*unstructured.Unstructured, ca []byte) error {
	for _, resource := range resources {
		if resource.GetKind() != "ValidatingWebhookConfiguration" {
			continue
		}
		webhooks, found, err := unstructured.NestedSlice(resource.Object, "webhooks")
		if err != nil || !found || len(webhooks) != 1 {
			return errors.New("liquid admission webhook configuration is invalid")
		}
		webhook, ok := webhooks[0].(map[string]any)
		if !ok {
			return errors.New("liquid admission webhook is invalid")
		}
		clientConfig, ok := webhook["clientConfig"].(map[string]any)
		if !ok {
			return errors.New("liquid admission webhook client configuration is invalid")
		}
		clientConfig["caBundle"] = base64.StdEncoding.EncodeToString(ca)
		return unstructured.SetNestedSlice(resource.Object, webhooks, "webhooks")
	}
	return errors.New("liquid admission webhook configuration is missing")
}

func toUnstructured(object runtime.Object) *unstructured.Unstructured {
	value, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: value}
}
