// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package static

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTenantNamespaceProfile(t *testing.T) {
	rendered, err := RenderTenantNamespaceProfile("preview-change-21")
	require.NoError(t, err)

	objects := decodeManifestObjects(t, rendered)
	require.Len(t, objects, 4)
	assert.Equal(t, "Namespace", objects[0].GetKind())
	assert.Equal(t, "preview-change-21", objects[0].GetName())
	assert.Equal(t, "restricted", objects[0].GetLabels()["pod-security.kubernetes.io/enforce"])
	assert.Equal(t, "true", objects[0].GetLabels()["liquid.oramasearch.com/environment"])

	assert.Equal(t, "NetworkPolicy", objects[1].GetKind())
	assert.Equal(t, "default-deny", objects[1].GetName())
	assert.Equal(t, "preview-change-21", objects[1].GetNamespace())

	assert.Equal(t, "ResourceQuota", objects[2].GetKind())
	assert.Equal(t, "preview-change-21", objects[2].GetNamespace())

	assert.Equal(t, "LimitRange", objects[3].GetKind())
	assert.Equal(t, "preview-change-21", objects[3].GetNamespace())
}

func TestRenderTenantNamespaceProfileRejectsInvalidName(t *testing.T) {
	_, err := RenderTenantNamespaceProfile("Not A Namespace")
	require.ErrorContains(t, err, "invalid namespace name")
}

func TestUntrustedRuntimeClass(t *testing.T) {
	objects := decodeManifestObjects(t, UntrustedRuntimeClass())
	require.Len(t, objects, 1)
	assert.Equal(t, "RuntimeClass", objects[0].GetKind())
	assert.Equal(t, "untrusted", objects[0].GetName())
	assert.Equal(t, "kata", objects[0].Object["handler"])

	nodeSelector, found, err := unstructured.NestedStringMap(objects[0].Object, "scheduling", "nodeSelector")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "kata", nodeSelector["liquid.oramasearch.com/runtime"])
}

func TestRenderLiquidPlatform(t *testing.T) {
	platform := &v1beta1.LiquidPlatform{
		ACMEDirectory: "https://lake.example/acme/customer/directory",
		LogsEndpoint:  "https://logs.example",
	}
	rendered, err := RenderLiquidPlatform(platform)
	require.NoError(t, err)

	objects := decodeManifestObjects(t, rendered)
	require.Len(t, objects, 8)
	assert.Equal(t, "Namespace", objects[0].GetKind())
	assert.Equal(t, "liquid-system", objects[0].GetName())
	assert.Equal(t, "GatewayClass", objects[1].GetKind())
	assert.Equal(t, "liquid", objects[1].GetName())
	assert.Equal(t, "Gateway", objects[2].GetKind())
	assert.Equal(t, "ClusterIssuer", objects[3].GetKind())
	assert.Equal(t, "lake-acme", objects[3].GetName())
	assert.Equal(t, "ValidatingAdmissionPolicy", objects[4].GetKind())
	assert.Equal(t, "liquid-environment-secrets", objects[4].GetName())
	assert.Equal(t, "ValidatingAdmissionPolicy", objects[6].GetKind())
	assert.Equal(t, "liquid-environment-pods", objects[6].GetName())
}

func TestRenderLiquidPlatformWithoutACME(t *testing.T) {
	rendered, err := RenderLiquidPlatform(&v1beta1.LiquidPlatform{
		LogsEndpoint: "https://logs.example",
	})
	require.NoError(t, err)

	objects := decodeManifestObjects(t, rendered)
	require.Len(t, objects, 7)
	for _, object := range objects {
		assert.NotEqual(t, "ClusterIssuer", object.GetKind())
	}
}

func decodeManifestObjects(t *testing.T, manifest []byte) []unstructured.Unstructured {
	t.Helper()

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	var objects []unstructured.Unstructured
	for {
		var object unstructured.Unstructured
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			return objects
		}
		require.NoError(t, err)
		objects = append(objects, object)
	}
}
