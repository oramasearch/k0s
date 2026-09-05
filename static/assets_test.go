// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package static

import (
	"bytes"
	"errors"
	"io"
	"testing"

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
