// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k0sproject/k0s/cmd"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"sigs.k8s.io/yaml"
)

func TestAirgapExportChartsPrintsOfflineConfig(t *testing.T) {
	cluster := v1beta1.DefaultClusterConfig()
	for _, entry := range cluster.Spec.Liquid.Components.Entries() {
		entry.Component.Enabled = entry.Name == v1beta1.LiquidComponentCilium
	}
	component := cluster.Spec.Liquid.Components.Cilium
	source := t.TempDir()
	_, err := chartutil.Save(&chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: "v2",
			Name:       component.Chart.Name,
			Version:    component.Version,
		},
		Templates: []*chart.File{{
			Name: "templates/pod.yaml",
			Data: []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: fixture\nspec:\n  containers:\n    - name: agent\n      image: registry.example.com/agent:v1\n"),
		}},
	}, source)
	require.NoError(t, err)
	input, err := yaml.Marshal(cluster)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "connected.yaml")
	require.NoError(t, os.WriteFile(configPath, input, 0600))
	output := new(strings.Builder)
	destination := filepath.Join(t.TempDir(), "exported")
	command := cmd.NewRootCmd()
	command.SetArgs([]string{
		"airgap", "export-charts", "--config=" + configPath,
		"--platform=linux/amd64", "--chart-dir=" + source,
		"--output=" + destination, "--repository=https://charts.internal.example/liquid",
	})
	command.SetOut(output)
	require.NoError(t, command.Execute())
	var offline v1beta1.ClusterConfig
	require.NoError(t, yaml.Unmarshal([]byte(output.String()), &offline))
	require.Equal(t, "https://charts.internal.example/liquid", offline.Spec.Liquid.Components.Cilium.Chart.Repository)
	require.Equal(t, cluster.Spec.Liquid.Components.GatewayAPI, offline.Spec.Liquid.Components.GatewayAPI)
	require.FileExists(t, filepath.Join(destination, "index.yaml"))
	require.FileExists(t, filepath.Join(destination, "charts.lock.json"))
	require.NoError(t, os.WriteFile(configPath, []byte(output.String()), 0600))
	output.Reset()
	command = cmd.NewRootCmd()
	command.SetArgs([]string{
		"airgap", "list-images", "--config=" + configPath,
		"--platform=linux/amd64", "--chart-dir=" + destination, "--all",
	})
	command.SetOut(output)
	require.NoError(t, command.Execute())
	require.Contains(t, output.String(), "registry.example.com/agent:v1\n")
	require.NotContains(t, output.String(), "/calico-")
}
