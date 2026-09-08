// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"
)

const maxLiquidChartDownload = 32 << 20

type liquidChart struct {
	name     string
	filename string
	archive  []byte
	chart    *chart.Chart
	images   []string
}

func loadLiquidChart(env TargetEnv, entry v1beta1.LiquidComponentEntry) (*liquidChart, error) {
	component := entry.Component
	filename := component.Chart.Name + "-" + component.Version + ".tgz"
	if component.Chart.Name == "" || component.Version == "" || filepath.Base(filename) != filename {
		return nil, fmt.Errorf("chart name and exact version must form a local archive filename")
	}
	var archive []byte
	var err error
	if env.ChartDir != "" {
		archive, err = os.ReadFile(filepath.Join(env.ChartDir, filename))
	} else {
		archive, err = downloadLiquidChart(env.Context, component)
	}
	if err != nil {
		return nil, err
	}
	loaded, err := loader.LoadArchive(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	if loaded.Metadata.Name != component.Chart.Name || loaded.Metadata.Version != component.Version {
		return nil, fmt.Errorf("archive declares %s %s; expected %s %s", loaded.Metadata.Name, loaded.Metadata.Version, component.Chart.Name, component.Version)
	}
	return &liquidChart{
		name:     entry.Name,
		filename: filename,
		archive:  archive,
		chart:    loaded,
	}, nil
}

func downloadLiquidChart(ctx context.Context, component *v1beta1.LiquidComponent) ([]byte, error) {
	indexURL, err := repo.ResolveReferenceURL(component.Chart.Repository, "index.yaml")
	if err != nil {
		return nil, err
	}
	data, err := downloadLiquidFile(ctx, indexURL)
	if err != nil {
		return nil, err
	}
	var index repo.IndexFile
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("decode chart repository index: %w", err)
	}
	var selected *repo.ChartVersion
	for _, version := range index.Entries[component.Chart.Name] {
		if version != nil && version.Metadata != nil && version.Version == component.Version {
			selected = version
			break
		}
	}
	if selected == nil || len(selected.URLs) == 0 {
		return nil, fmt.Errorf("repository has no archive for %s at exact version %s", component.Chart.Name, component.Version)
	}
	archiveURL, err := repo.ResolveReferenceURL(component.Chart.Repository, selected.URLs[0])
	if err != nil {
		return nil, err
	}
	archive, err := downloadLiquidFile(ctx, archiveURL)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(archive)
	if selected.Digest == "" || !strings.EqualFold(strings.TrimPrefix(selected.Digest, "sha256:"), hex.EncodeToString(digest[:])) {
		return nil, fmt.Errorf("chart archive SHA-256 does not match repository index")
	}
	return archive, nil
}

func downloadLiquidFile(ctx context.Context, address string) ([]byte, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("chart downloads require an HTTPS URL: %s", address)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" || len(via) >= 10 {
				return fmt.Errorf("chart download redirected outside HTTPS or exceeded redirect limit")
			}
			return nil
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", address, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxLiquidChartDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLiquidChartDownload {
		return nil, fmt.Errorf("chart download exceeds %d bytes", maxLiquidChartDownload)
	}
	return data, nil
}
