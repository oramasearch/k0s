// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	imagespecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/chart/loader"
	"sigs.k8s.io/yaml"
)

var defaultChartArchives struct {
	sync.Once
	archives map[string][]byte
	err      error
}

// Exact upstream archives are fetched once for the chart comparison checks.
// A pre-transferred directory makes the entire suite independent of public egress.
func defaultLiquidTarget(t *testing.T) TargetEnv {
	t.Helper()
	defaultChartArchives.Do(func() {
		defaultChartArchives.archives = map[string][]byte{}
		env := TargetEnv{ChartDir: os.Getenv("K0S_AIRGAP_CHART_DIR")}
		for _, entry := range v1beta1.DefaultLiquidSpec().Components.Entries() {
			component, err := loadLiquidChart(env, entry)
			if err != nil {
				defaultChartArchives.err = err
				return
			}
			defaultChartArchives.archives[component.filename] = component.archive
		}
	})
	require.NoError(t, defaultChartArchives.err)
	directory := t.TempDir()
	for filename, archive := range defaultChartArchives.archives {
		require.NoError(t, os.WriteFile(filepath.Join(directory, filename), archive, 0644))
	}
	return TargetEnv{
		Platform: imagespecv1.Platform{OS: "linux", Architecture: "amd64"},
		Spec:     v1beta1.DefaultClusterSpec(),
		ChartDir: directory,
		Context:  t.Context(),
	}
}

func TestGetImageURIsIncludesManagedLiquidComponents(t *testing.T) {
	images, err := GetImageURIs(defaultLiquidTarget(t), true)
	require.NoError(t, err)
	for _, component := range []string{
		"cilium/cilium:",
		"cert-manager-controller:",
		"timberio/vector:",
		"rancher/local-path-provisioner:",
		"longhornio/longhorn-manager:",
	} {
		found := false
		for _, image := range images {
			if strings.Contains(image, component) {
				found = true
			}
		}
		if !found {
			t.Errorf("declared managed component missing from inventory: %s", component)
		}
	}
	for _, image := range images {
		require.NotContains(t, image, "/calico-")
		require.NotContains(t, image, "/kube-router:")
		require.NotContains(t, image, "/cni-node:")
	}
}

func TestLiquidInventoryMatchesRenderedCharts(t *testing.T) {
	env := defaultLiquidTarget(t)
	inventory, err := liquidImageInventory(env)
	require.NoError(t, err)
	entries := env.Spec.Liquid.Components.Entries()
	require.Len(t, entries, reflect.TypeFor[v1beta1.LiquidComponents]().NumField(), "every declared field must be enumerated")
	require.Len(t, inventory, len(entries), "every managed chart must be inventoried, including charts without Pods")
	for index, entry := range entries {
		t.Run(entry.Name, func(t *testing.T) {
			component := inventory[index]
			require.Equal(t, entry.Name, component.name)
			archive, err := loader.Load(filepath.Join(env.ChartDir, component.filename))
			require.NoError(t, err)
			var values map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(env.Spec.Liquid.LiquidComponentValues(entry.Name)), &values))
			for _, upgrade := range []bool{false, true} {
				manifests, err := renderLiquidChart(archive, entry.Name, values, upgrade)
				require.NoError(t, err)
				// Check raw rendered image lines independently of the YAML inventory
				// walker. This also finds images inside ConfigMap Pod templates.
				for filename, manifest := range manifests {
					for _, line := range strings.Split(manifest, "\n") {
						line = strings.TrimSpace(line)
						if !strings.HasPrefix(line, "image:") {
							continue
						}
						image := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "image:")), "\"'")
						if image != "" {
							require.Contains(t, component.images, image, "%s: rendered image omitted", filename)
						}
					}
				}
			}
			if entry.Name == v1beta1.LiquidComponentGatewayAPI {
				require.Empty(t, component.images, "Gateway API packages CRDs only")
			} else {
				require.NotEmpty(t, component.images, "a new component cannot silently contribute no images")
			}
			t.Logf("%s %s: %d images", entry.Name, entry.Component.Version, len(component.images))
		})
	}
}

func TestLiquidAncillaryImages(t *testing.T) {
	images, err := GetImageURIs(defaultLiquidTarget(t), true)
	require.NoError(t, err)
	for _, image := range []string{
		"quay.io/jetstack/cert-manager-acmesolver:v1.21.1",
		"quay.io/jetstack/cert-manager-startupapicheck:v1.21.1",
		"docker.io/library/busybox:latest",
		"docker.io/rancher/local-path-provisioner:v0.0.37",
		"docker.io/longhornio/backing-image-manager:v1.12.1",
		"docker.io/longhornio/csi-attacher:v4.12.0",
		"docker.io/longhornio/csi-node-driver-registrar:v2.17.0",
		"docker.io/longhornio/csi-provisioner:v5.3.0",
		"docker.io/longhornio/csi-resizer:v2.2.1",
		"docker.io/longhornio/csi-snapshotter:v8.6.0",
		"docker.io/longhornio/livenessprobe:v2.19.0",
		"docker.io/longhornio/longhorn-engine:v1.12.1",
		"docker.io/longhornio/longhorn-instance-manager:v1.12.1",
		"docker.io/longhornio/longhorn-share-manager:v1.12.1",
		"docker.io/longhornio/support-bundle-kit:v0.0.92",
	} {
		require.Contains(t, images, image)
	}
}

func TestLiquidDisabledAndExternalComponents(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, replacement := range []string{"disabled", v1beta1.LiquidReplacementExternal} {
			t.Run(fmt.Sprintf("%s/all=%t", replacement, all), func(t *testing.T) {
				env := defaultLiquidTarget(t)
				for _, entry := range env.Spec.Liquid.Components.Entries() {
					if replacement == "disabled" {
						entry.Component.Enabled = false
					} else {
						entry.Component.Replacement = replacement
					}
				}
				env.ChartDir = filepath.Join(t.TempDir(), "must-not-be-read")
				images, err := GetImageURIs(env, all)
				require.NoError(t, err)
				require.Equal(t, getBaseImageURIs(env, all), images)
			})
		}
	}
}

func TestLiquidValuesSelectImages(t *testing.T) {
	env := defaultLiquidTarget(t)
	components := env.Spec.Liquid.Components
	components.Cilium.Values += "extraInitContainers:\n  - name: custom-init\n    image: registry.example.com/init:v2\n"
	components.CertManager.Values += "imageRegistry: registry.example.com\nacmesolver:\n  image:\n    tag: custom-solver\nstartupapicheck:\n  enabled: false\n"
	components.LocalPath.Values += "helperImage:\n  repository: registry.example.com/helper\n  tag: v2\n"
	components.DistributedStorage.Values += "global:\n  imageRegistry: registry.example.com\n"
	images, err := GetImageURIs(env, false)
	require.NoError(t, err)
	require.Contains(t, images, "registry.example.com/init:v2")
	require.Contains(t, images, "registry.example.com/jetstack/cert-manager-acmesolver:custom-solver")
	require.Contains(t, images, "registry.example.com/helper:v2")
	require.Contains(t, images, "registry.example.com/longhornio/csi-attacher:v4.12.0")
	require.NotContains(t, strings.Join(images, "\n"), "cert-manager-startupapicheck:")
	require.NotContains(t, images, "docker.io/library/busybox:latest")
}

func TestLiquidArchitectureAndLegacyInventory(t *testing.T) {
	env := defaultLiquidTarget(t)
	amd64, err := GetImageURIs(env, true)
	require.NoError(t, err)
	env.Platform.Architecture = "arm64"
	arm64, err := GetImageURIs(env, true)
	require.NoError(t, err)
	require.Equal(t, amd64, arm64, "the declared chart references use multi-platform tags/digests")
	for _, arch := range []string{"arm", "riscv64"} {
		env.Platform.Architecture = arch
		images, err := GetImageURIs(env, true)
		require.ErrorContains(t, err, "linux/amd64 or linux/arm64")
		require.Nil(t, images)
	}
	env.Platform = imagespecv1.Platform{OS: "windows", Architecture: "amd64"}
	images, err := GetImageURIs(env, true)
	require.NoError(t, err)
	require.Equal(t, getBaseImageURIs(env, true), images, "Linux charts do not run on Windows nodes")
	env.Platform = imagespecv1.Platform{OS: "linux", Architecture: "amd64"}
	env.Spec.Liquid = nil
	images, err = GetImageURIs(env, true)
	require.NoError(t, err)
	require.Len(t, images, 13)
	require.Contains(t, images, env.Spec.Images.Calico.CNI.URI())
	require.Contains(t, images, env.Spec.Images.KubeRouter.CNI.URI())
}

func TestLiquidInventoryRejectsIncompleteInputs(t *testing.T) {
	for _, change := range []string{"version", "archive", "values", "replacement", "controller-fallback", "missing-components"} {
		t.Run(change, func(t *testing.T) {
			env := defaultLiquidTarget(t)
			switch change {
			case "version":
				env.Spec.Liquid.Components.Cilium.Version = "not-packaged"
			case "archive":
				env.ChartDir = t.TempDir()
			case "values":
				env.Spec.Liquid.Components.Cilium.Values = "broken: ["
			case "missing-components":
				env.Spec.Liquid.Components = nil
			case "controller-fallback":
				env.Spec.Liquid.Components.DistributedStorage.Values = "image:\n  csi:\n    attacher:\n      tag: ''\n"
			case "replacement":
				env.Spec.Liquid.Components.Cilium.Replacement = "unknown"
			}
			images, err := GetImageURIs(env, true)
			require.Error(t, err)
			require.Nil(t, images, "a partial base inventory would produce an incomplete bundle")
		})
	}
}

func TestImagesFromManifest(t *testing.T) {
	manifest := `apiVersion: v1
kind: Pod
metadata:
  annotations:
    helm.sh/hook: pre-install
spec:
  initContainers:
    - name: init
      image: example.com/init:v1
  containers:
    - name: controller
      image: example.com/controller:v1
      args:
        - --solver-image=example.com/solver:v1
        - --helper-image
        - example.com/helper:v1
      env:
        - name: CSI_ATTACHER_IMAGE
          value: example.com/attacher:v1
---
apiVersion: v1
kind: ConfigMap
data:
  helperPod.yaml: |
    spec:
      containers:
        - name: helper
          image: example.com/dynamic:v1
`
	images, err := imagesFromManifest(manifest)
	require.NoError(t, err)
	slices.Sort(images)
	require.Equal(t, []string{
		"example.com/attacher:v1",
		"example.com/controller:v1",
		"example.com/dynamic:v1",
		"example.com/helper:v1",
		"example.com/init:v1",
		"example.com/solver:v1",
	}, images)
	for _, bad := range []string{
		"image: unversioned",
		"image: ''",
		"name: CSI_IMAGE\nvalueFrom:\n  secretKeyRef: {}",
		"args: ['--helper-image']",
	} {
		_, err := imagesFromManifest(bad)
		require.Error(t, err)
	}
}
