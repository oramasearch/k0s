// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidadmission

import (
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticPodManifestUsesEmbeddedBinaries(t *testing.T) {
	vars := &config.CfgVars{BinDir: filepath.Join(t.TempDir(), "bin")}
	reconciler := NewReconciler(vars, nil, &v1beta1.ImageSpec{Image: "pause", Version: "latest"})
	manifest, err := reconciler.manifest()
	require.NoError(t, err)
	require.Len(t, manifest.Spec.Containers, 2)
	assert.Equal(t, "/usr/local/bin/liquid", manifest.Spec.Containers[0].Command[0])
	assert.Equal(t, "/usr/local/bin/k0s", manifest.Spec.Containers[1].Command[0])
	assert.True(t, manifest.Spec.HostNetwork)
	assert.Equal(t, "liquid-admission", manifest.Labels["app.kubernetes.io/component"])
	assert.Equal(t, filepath.Join(vars.BinDir, "liquid"), manifest.Spec.Volumes[0].HostPath.Path)
	assert.Contains(t, manifest.Spec.Containers[1].Args, "liquid-admission.kube-system.svc")
}
