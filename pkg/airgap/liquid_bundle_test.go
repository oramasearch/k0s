// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"
)

func TestExportLiquidCharts(t *testing.T) {
	env := defaultLiquidTarget(t)
	original := env.Spec.DeepCopy()
	images, err := GetImageURIs(env, true)
	require.NoError(t, err)
	directory := filepath.Join(t.TempDir(), "charts")
	result, err := ExportLiquidCharts(env, directory, "https://charts.internal.example/liquid")
	require.NoError(t, err)
	require.Equal(t, original, env.Spec)
	for _, entry := range result.Liquid.Components.Entries() {
		require.Equal(t, "https://charts.internal.example/liquid", entry.Component.Chart.Repository)
	}
	index, err := repo.LoadIndexFile(filepath.Join(directory, "index.yaml"))
	require.NoError(t, err)
	require.Len(t, index.Entries, 6)
	for _, entry := range result.Liquid.Components.Entries() {
		version, err := index.Get(entry.Component.Chart.Name, entry.Component.Version)
		require.NoError(t, err)
		require.Len(t, version.URLs, 1)
		require.Equal(t, filepath.Base(version.URLs[0]), version.URLs[0], "archive URLs must resolve within the internal repository")
		archive, err := os.ReadFile(filepath.Join(directory, version.URLs[0]))
		require.NoError(t, err)
		digest := sha256.Sum256(archive)
		require.Equal(t, hex.EncodeToString(digest[:]), version.Digest)
	}
	data, err := os.ReadFile(filepath.Join(directory, "charts.lock.json"))
	require.NoError(t, err)
	var records []liquidChartRecord
	require.NoError(t, json.Unmarshal(data, &records))
	require.Len(t, records, 6)
	require.Equal(t, v1beta1.LiquidComponentGatewayAPI, records[1].Component)
	require.Empty(t, records[1].Images)

	// No network fallback: the invented internal hostname cannot resolve, yet
	// the exported config and archives reproduce exactly the staged inventory.
	env.Spec = result
	env.ChartDir = directory
	offline, err := GetImageURIs(env, true)
	require.NoError(t, err)
	require.Equal(t, images, offline)
}

func TestExportLiquidChartsSkipsUnmanagedComponents(t *testing.T) {
	env := defaultLiquidTarget(t)
	env.Spec.Liquid.Components.Cilium.Replacement = v1beta1.LiquidReplacementExternal
	env.Spec.Liquid.Components.GatewayAPI.Enabled = false
	directory := filepath.Join(t.TempDir(), "charts")
	result, err := ExportLiquidCharts(env, directory, "https://charts.internal.example/liquid")
	require.NoError(t, err)
	require.Equal(t, env.Spec.Liquid.Components.Cilium, result.Liquid.Components.Cilium)
	require.Equal(t, env.Spec.Liquid.Components.GatewayAPI, result.Liquid.Components.GatewayAPI)
	index, err := repo.LoadIndexFile(filepath.Join(directory, "index.yaml"))
	require.NoError(t, err)
	require.Len(t, index.Entries, 4)
	require.NotContains(t, index.Entries, "cilium")
	require.NotContains(t, index.Entries, "gateway-api-crds")
}

func TestExportLiquidChartsRejectsExistingDirectory(t *testing.T) {
	env := defaultLiquidTarget(t)
	directory := t.TempDir()
	marker := filepath.Join(directory, "keep")
	require.NoError(t, os.WriteFile(marker, []byte("original"), 0600))
	_, err := ExportLiquidCharts(env, directory, "https://charts.internal.example")
	require.Error(t, err)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}

func TestDownloadLiquidChartChecksDigestAndVersion(t *testing.T) {
	env := defaultLiquidTarget(t)
	entry := env.Spec.Liquid.Components.Entries()[0]
	component, err := loadLiquidChart(env, entry)
	require.NoError(t, err)
	digest := sha256.Sum256(component.archive)
	index := repo.NewIndexFile()
	require.NoError(t, index.MustAdd(component.chart.Metadata, component.filename, "", hex.EncodeToString(digest[:])))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/charts/index.yaml" {
			data, err := yaml.Marshal(index)
			require.NoError(t, err)
			_, _ = w.Write(data)
			return
		}
		if r.URL.Path == "/charts/"+component.filename {
			_, _ = w.Write(component.archive)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() {
		http.DefaultTransport = originalTransport
	}()
	entry.Component.Chart.Repository = server.URL + "/charts"
	env.ChartDir = ""
	loaded, err := loadLiquidChart(env, entry)
	require.NoError(t, err)
	require.Equal(t, component.archive, loaded.archive)
	index.Entries[entry.Component.Chart.Name][0].Digest = "incorrect"
	_, err = loadLiquidChart(env, entry)
	require.ErrorContains(t, err, "SHA-256")
	entry.Component.Version = "1.20.999"
	_, err = loadLiquidChart(env, entry)
	require.ErrorContains(t, err, "exact version")
	_, err = downloadLiquidFile(t.Context(), "http://public.example/chart.tgz")
	require.ErrorContains(t, err, "HTTPS")
}

func TestLoadLiquidChartRejectsMislabeledArchive(t *testing.T) {
	env := defaultLiquidTarget(t)
	entry := env.Spec.Liquid.Components.Entries()[0]
	original, err := os.ReadFile(filepath.Join(env.ChartDir, "cilium-1.20.1.tgz"))
	require.NoError(t, err)
	entry.Component.Version = "1.20.999"
	require.NoError(t, os.WriteFile(filepath.Join(env.ChartDir, "cilium-1.20.999.tgz"), original, 0644))
	_, err = loadLiquidChart(env, entry)
	require.ErrorContains(t, err, "archive declares cilium 1.20.1; expected cilium 1.20.999")
}
