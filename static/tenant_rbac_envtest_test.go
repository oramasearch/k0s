//go:build envtest

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package static

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tenantProofNamespace = "tenant-rbac-proof"

func TestTenantProfileRBAC(t *testing.T) {
	environment := &envtest.Environment{
		UseExistingCluster:          ptr.To(false),
		DownloadBinaryAssets:        os.Getenv("KUBEBUILDER_ASSETS") == "",
		DownloadBinaryAssetsVersion: "1.30.3",
		CRDs:                        []*apiextensionsv1.CustomResourceDefinition{sealedSecretProofCRD()},
	}
	environment.ControlPlane.GetAPIServer().Configure().Set("authorization-mode", "RBAC")
	config, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, environment.Stop())
	})
	admin, err := client.New(config, client.Options{})
	require.NoError(t, err)
	binding := installTenantProofProfile(t, admin)
	installTenantProofPolicies(t, admin)

	user, err := environment.AddUser(envtest.User{
		Name:   "tenant-proof",
		Groups: []string{binding.Subjects[0].Name},
	}, nil)
	require.NoError(t, err)
	tenant, err := client.New(user.Config(), client.Options{})
	require.NoError(t, err)
	operator := tenantProofOperator(t, admin, config)
	waitForTenantProofAccess(t, tenant, "configmaps")
	waitForTenantProofAccess(t, operator, "secrets")
	waitForTenantProofPolicies(t, operator, tenant)

	t.Run("secret_boundary", func(t *testing.T) {
		proveTenantSecretBoundary(t, admin, tenant, operator)
	})
	t.Run("workloads", func(t *testing.T) {
		proveTenantWorkloads(t, tenant)
	})
	t.Run("permissions", func(t *testing.T) {
		proveTenantPermissions(t, tenant)
	})
}

func sealedSecretProofCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "lakesealedsecrets.orama.cloud"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "orama.cloud",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   "lakesealedsecrets",
				Singular: "lakesealedsecret",
				Kind:     "LakeSealedSecret",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1alpha1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
				},
			}},
		},
	}
}

func installTenantProofProfile(t *testing.T, admin client.Client) *rbacv1.RoleBinding {
	t.Helper()
	profile, err := RenderTenantNamespaceProfile(tenantProofNamespace)
	require.NoError(t, err)
	for _, object := range decodeManifestObjects(t, profile) {
		require.NoError(t, admin.Create(t.Context(), &object))
	}
	binding := &rbacv1.RoleBinding{}
	key := client.ObjectKey{Namespace: tenantProofNamespace, Name: "liquid-tenant"}
	require.NoError(t, admin.Get(t.Context(), key, binding))
	require.Equal(t, []rbacv1.Subject{{
		Kind: "Group", APIGroup: rbacv1.GroupName,
		Name: "liquid:tenant:" + tenantProofNamespace,
	}}, binding.Subjects)
	require.Equal(t, rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName, Kind: "Role", Name: "liquid-tenant",
	}, binding.RoleRef)

	// envtest has no quota controller to initialize usage before admission.
	quota := &corev1.ResourceQuota{}
	key.Name = "tenant-defaults"
	require.NoError(t, admin.Get(t.Context(), key, quota))
	quota.Status.Hard = quota.Spec.Hard.DeepCopy()
	quota.Status.Used = corev1.ResourceList{}
	for name := range quota.Status.Hard {
		quota.Status.Used[name] = resource.MustParse("0")
	}
	require.NoError(t, admin.Status().Update(t.Context(), quota))
	for _, object := range decodeManifestObjects(t, UntrustedRuntimeClass()) {
		require.NoError(t, admin.Create(t.Context(), &object))
	}
	return binding
}

func installTenantProofPolicies(t *testing.T, admin client.Client) {
	t.Helper()
	platform, err := RenderLiquidPlatform(v1beta1.DefaultClusterConfig().Spec.Liquid.Platform)
	require.NoError(t, err)
	policies := 0
	for _, object := range decodeManifestObjects(t, platform) {
		if object.GetAPIVersion() != admissionv1.SchemeGroupVersion.String() {
			continue
		}
		require.NoError(t, admin.Create(t.Context(), &object))
		policies++
	}
	require.Equal(t, 4, policies)
}

func tenantProofOperator(t *testing.T, admin client.Client, config *rest.Config) client.Client {
	t.Helper()
	require.NoError(t, admin.Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "orama-system"},
	}))
	require.NoError(t, admin.Create(t.Context(), &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "orama-operator", Namespace: "orama-system"},
	}))
	// The operator's separately managed RBAC is represented by a minimal fixture.
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "operator-delivery", Namespace: tenantProofNamespace},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"secrets"},
			Verbs: []string{"get", "create", "update"},
		}},
	}
	require.NoError(t, admin.Create(t.Context(), role))
	require.NoError(t, admin.Create(t.Context(), &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: role.Namespace},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "orama-operator", Namespace: "orama-system"}},
	}))
	bootstrap, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)
	token, err := bootstrap.CoreV1().ServiceAccounts("orama-system").CreateToken(
		t.Context(), "orama-operator", &authenticationv1.TokenRequest{}, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	operator, err := client.New(&rest.Config{
		Host:            config.Host,
		TLSClientConfig: rest.TLSClientConfig{CAData: config.CAData},
		BearerToken:     token.Status.Token,
	}, client.Options{})
	require.NoError(t, err)
	return operator
}

func tenantProofAllowed(ctx context.Context, actor client.Client, attributes authorizationv1.ResourceAttributes) (bool, error) {
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &attributes},
	}
	err := actor.Create(ctx, review)
	return review.Status.Allowed, err
}

func waitForTenantProofAccess(t *testing.T, actor client.Client, resourceName string) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		allowed, err := tenantProofAllowed(t.Context(), actor, authorizationv1.ResourceAttributes{
			Namespace: tenantProofNamespace, Resource: resourceName, Verb: "create",
		})
		require.NoError(c, err)
		require.True(c, allowed, "expected create access to %s", resourceName)
	}, 30*time.Second, 100*time.Millisecond)
}

func waitForTenantProofPolicies(t *testing.T, operator, tenant client.Client) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "policy-probe", Namespace: tenantProofNamespace,
		}}
		err := operator.Create(t.Context(), secret, client.DryRunAll)
		require.True(c, apierrors.IsInvalid(err), "expected admission Invalid response, got %v", err)
		require.ErrorContains(c, err, "liquid-environment-secrets")
	}, 30*time.Second, 100*time.Millisecond)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		pod := tenantProofPod("policy-probe")
		pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "database"}},
		}}
		err := tenant.Create(t.Context(), pod, client.DryRunAll)
		require.True(c, apierrors.IsInvalid(err), "expected admission Invalid response, got %v", err)
		require.ErrorContains(c, err, "liquid-environment-pods")
	}, 30*time.Second, 100*time.Millisecond)
	t.Log("Admission active: unlabelled Secret and unannotated Secret-consuming Pod refused")
}

func tenantProofSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "database", Namespace: tenantProofNamespace,
			Labels: map[string]string{"lake.orama.cloud/sealed": "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "orama.cloud/v1alpha1", Kind: "LakeSealedSecret",
				Name: "does-not-exist", UID: types.UID("00000000-0000-0000-0000-000000000040"),
				Controller: ptr.To(true),
			}},
		},
		Data: map[string][]byte{"password": []byte("proof-only")},
	}
}

func proveTenantSecretBoundary(t *testing.T, admin, tenant, operator client.Client) {
	t.Helper()
	owner := &unstructured.Unstructured{}
	owner.SetAPIVersion("orama.cloud/v1alpha1")
	owner.SetKind("LakeSealedSecret")
	require.True(t, apierrors.IsNotFound(admin.Get(t.Context(), client.ObjectKey{
		Namespace: tenantProofNamespace, Name: "does-not-exist",
	}, owner)))
	secret := tenantProofSecret()
	err := tenant.Create(t.Context(), secret.DeepCopy())
	requireTenantRBACRefusal(t, err, "create")
	t.Logf("Tenant forgery REFUSED (claimed LakeSealedSecret does not exist): %v", err)
	plain := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: tenantProofNamespace}}
	requireTenantRBACRefusal(t, tenant.Create(t.Context(), plain), "create")

	// The same payload reaches admission only for the authorized operator identity.
	// This deliberately proves that CEL does not check whether the owner exists.
	require.NoError(t, operator.Create(t.Context(), secret))
	secret.Data["password"] = []byte("updated-proof-only")
	require.NoError(t, operator.Update(t.Context(), secret))
	t.Log("Operator system:serviceaccount:orama-system:orama-operator CREATE and UPDATE succeeded")
	stored := &corev1.Secret{}
	require.NoError(t, operator.Get(t.Context(), client.ObjectKeyFromObject(secret), stored))
	require.Equal(t, secret.Data, stored.Data)
	requireTenantRBACRefusal(t, tenant.Get(t.Context(), client.ObjectKeyFromObject(secret), &corev1.Secret{}), "get")
	requireTenantRBACRefusal(t, tenant.List(t.Context(), &corev1.SecretList{}, client.InNamespace(tenantProofNamespace)), "list")
	requireTenantRBACRefusal(t, tenant.Update(t.Context(), secret.DeepCopy()), "update")
	requireTenantRBACRefusal(t, tenant.Patch(t.Context(), secret.DeepCopy(), client.RawPatch(
		types.MergePatchType, []byte(`{"data":{"password":"dGVuYW50"}}`),
	)), "patch")
	requireTenantRBACRefusal(t, tenant.Delete(t.Context(), secret.DeepCopy()), "delete")
	requireTenantRBACRefusal(t, tenant.DeleteAllOf(t.Context(), &corev1.Secret{}, client.InNamespace(tenantProofNamespace)), "deletecollection")
	t.Log("Tenant Secret get, list, update, patch, delete, and deletecollection REFUSED by RBAC")
}

func requireTenantRBACRefusal(t *testing.T, err error, verb string) {
	t.Helper()
	require.True(t, apierrors.IsForbidden(err), "expected RBAC Forbidden, got %v", err)
	require.ErrorContains(t, err, `User "tenant-proof" cannot `+verb+` resource "secrets"`)
}

func tenantProofPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: tenantProofNamespace},
		Spec: corev1.PodSpec{
			RuntimeClassName:             ptr.To("untrusted"),
			AutomountServiceAccountToken: ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name: "app", Image: "registry.k8s.io/pause:3.10",
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
}

func proveTenantWorkloads(t *testing.T, tenant client.Client) {
	t.Helper()
	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: tenantProofNamespace},
		Data:       map[string]string{"mode": "preview"},
	}
	require.NoError(t, tenant.Create(t.Context(), config))
	require.NoError(t, tenant.Get(t.Context(), client.ObjectKeyFromObject(config), config))
	config.Data["mode"] = "production"
	require.NoError(t, tenant.Update(t.Context(), config))
	require.NoError(t, tenant.Patch(t.Context(), config, client.RawPatch(
		types.MergePatchType, []byte(`{"data":{"mode":"preview"}}`),
	)))
	require.NoError(t, tenant.List(t.Context(), &corev1.ConfigMapList{}, client.InNamespace(tenantProofNamespace)))
	pod := tenantProofPod("app")
	require.NoError(t, tenant.Create(t.Context(), pod))
	require.NoError(t, tenant.Get(t.Context(), client.ObjectKeyFromObject(pod), &corev1.Pod{}))
	require.NoError(t, tenant.Create(t.Context(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: tenantProofNamespace},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}))
	require.NoError(t, tenant.Get(t.Context(), client.ObjectKey{
		Namespace: tenantProofNamespace, Name: "tenant-defaults",
	}, &corev1.ResourceQuota{}))
	require.NoError(t, tenant.Delete(t.Context(), pod))
	require.NoError(t, tenant.Delete(t.Context(), config))
	t.Log("Tenant workloads succeeded: ConfigMap create/get/list/update/patch/delete, restricted untrusted Pod create/get/delete, Service create, quota read")
}

func proveTenantPermissions(t *testing.T, tenant client.Client) {
	t.Helper()
	for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete", "deletecollection"} {
		allowed, err := tenantProofAllowed(t.Context(), tenant, authorizationv1.ResourceAttributes{
			Namespace: tenantProofNamespace, Resource: "secrets", Verb: verb,
		})
		require.NoError(t, err)
		require.False(t, allowed, "tenant must not %s secrets", verb)
	}
	denied := []authorizationv1.ResourceAttributes{
		{Group: rbacv1.GroupName, Resource: "roles", Verb: "create"},
		{Group: rbacv1.GroupName, Resource: "roles", Verb: "escalate"},
		{Group: rbacv1.GroupName, Resource: "rolebindings", Verb: "create"},
		{Group: rbacv1.GroupName, Resource: "roles", Verb: "bind"},
		{Resource: "serviceaccounts", Verb: "create"},
		{Resource: "serviceaccounts", Subresource: "token", Verb: "create"},
		{Resource: "pods", Subresource: "exec", Verb: "create"},
		{Resource: "pods", Subresource: "ephemeralcontainers", Verb: "update"},
		{Resource: "resourcequotas", Verb: "update"},
		{Resource: "limitranges", Verb: "update"},
		{Group: "networking.k8s.io", Resource: "networkpolicies", Verb: "delete"},
	}
	for _, attributes := range denied {
		attributes.Namespace = tenantProofNamespace
		allowed, err := tenantProofAllowed(t.Context(), tenant, attributes)
		require.NoError(t, err)
		require.False(t, allowed, "unexpected tenant permission: %+v", attributes)
	}
	for _, attributes := range []authorizationv1.ResourceAttributes{
		{Namespace: "other-environment", Resource: "pods", Verb: "create"},
		{Resource: "namespaces", Verb: "update"},
		{Resource: "nodes", Verb: "get"},
		{Group: rbacv1.GroupName, Resource: "clusterrolebindings", Verb: "create"},
	} {
		allowed, err := tenantProofAllowed(t.Context(), tenant, attributes)
		require.NoError(t, err)
		require.False(t, allowed, "unexpected tenant permission: %+v", attributes)
	}
	t.Log("Tenant Secret watch, privilege escalation, policy changes, and cross-namespace/cluster access REFUSED")
}
