// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"helm.sh/helm/v3/pkg/repo"
)

type liquidChartRecord struct {
	Component string   `json:"component"`
	Archive   string   `json:"archive"`
	Version   string   `json:"version"`
	Source    string   `json:"source"`
	SHA256    string   `json:"sha256"`
	Images    []string `json:"images"`
}

// ExportLiquidCharts writes the exact managed charts, an internal Helm repository
// index and an audit inventory to a new directory. The returned spec redirects all
// exported components to repositoryURL. The caller's spec is unchanged.
func ExportLiquidCharts(env TargetEnv, directory, repositoryURL string) (_ *v1beta1.ClusterSpec, err error) {
	parsed, parseErr := url.Parse(repositoryURL)
	if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("internal chart repository must be an HTTPS URL without a query or fragment")
	}
	if env.Spec == nil || env.Platform.OS != "linux" {
		return nil, fmt.Errorf("chart export requires a cluster spec and a Linux target platform")
	}
	charts, err := liquidImageInventory(env)
	if err != nil {
		return nil, err
	}
	if len(charts) == 0 {
		return nil, fmt.Errorf("no enabled managed liquid components to export")
	}
	if err := os.Mkdir(directory, 0755); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(directory)
		}
	}()
	return writeLiquidCharts(env.Spec, charts, directory, repositoryURL)
}

func writeLiquidCharts(spec *v1beta1.ClusterSpec, charts []*liquidChart, directory, repositoryURL string) (*v1beta1.ClusterSpec, error) {
	result := spec.DeepCopy()
	index := repo.NewIndexFile()
	archives := map[string][]byte{}
	var records []liquidChartRecord
	for _, component := range charts {
		if previous, ok := archives[component.filename]; ok && !bytes.Equal(previous, component.archive) {
			return nil, fmt.Errorf("different charts use the same archive filename: %s", component.filename)
		}
		if err := os.WriteFile(filepath.Join(directory, component.filename), component.archive, 0644); err != nil {
			return nil, err
		}
		digest := sha256.Sum256(component.archive)
		checksum := hex.EncodeToString(digest[:])
		if _, ok := archives[component.filename]; !ok {
			if err := index.MustAdd(component.chart.Metadata, component.filename, "", checksum); err != nil {
				return nil, err
			}
		}
		archives[component.filename] = component.archive
		for _, entry := range result.Liquid.Components.Entries() {
			if entry.Name != component.name {
				continue
			}
			records = append(records, liquidChartRecord{
				Component: component.name,
				Archive:   component.filename,
				Version:   entry.Component.Version,
				Source:    entry.Component.Chart.Repository,
				SHA256:    checksum,
				Images:    component.images,
			})
			entry.Component.Chart.Repository = repositoryURL
		}
	}
	index.SortEntries()
	if err := index.WriteFile(filepath.Join(directory, "index.yaml"), 0644); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(directory, "charts.lock.json"), append(data, '\n'), 0644); err != nil {
		return nil, err
	}
	return result, nil
}
