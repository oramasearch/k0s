// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePodReference(t *testing.T) {
	namespace, pod, err := parsePodReference("preview-change-21/api-7b9d8f")
	require.NoError(t, err)
	assert.Equal(t, "preview-change-21", namespace)
	assert.Equal(t, "api-7b9d8f", pod)

	_, _, err = parsePodReference("preview-change-21")
	require.ErrorContains(t, err, "NAMESPACE/POD")
}

func TestQueryLoki(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/loki/api/v1/query_range", request.URL.Path)
		assert.Equal(t, `{namespace="preview-change-21",pod="api-7b9d8f"}`, request.URL.Query().Get("query"))
		assert.Equal(t, "25", request.URL.Query().Get("limit"))
		assert.Equal(t, "backward", request.URL.Query().Get("direction"))
		assert.Equal(t, fmt.Sprint(now.Add(-time.Minute).UnixNano()), request.URL.Query().Get("start"))
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "status": "success",
  "data": {
    "result": [
      {
        "stream": {"container": "api"},
        "values": [["1800000000000000000", "ready"], ["1799999999000000000", "starting"]]
      }
    ]
  }
}`))
	}))
	defer server.Close()

	entries, err := queryLoki(
		t.Context(),
		server.Client(),
		server.URL,
		"preview-change-21",
		"api-7b9d8f",
		logQueryOptions{limit: 25, since: time.Minute},
		now,
	)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "starting", entries[0].line)
	assert.Equal(t, "ready", entries[1].line)

	var output strings.Builder
	require.NoError(t, printLogEntries(&output, entries))
	assert.Contains(t, output.String(), "api\tstarting")
}

func TestQueryLokiReportsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := queryLoki(
		t.Context(),
		server.Client(),
		server.URL,
		"preview-change-21",
		"api",
		logQueryOptions{limit: 1},
		time.Now(),
	)
	require.ErrorContains(t, err, "503 Service Unavailable")
}

func TestNewLogsAccessUsesKubernetesServiceProxy(t *testing.T) {
	access, err := newLogsAccess(
		&rest.Config{Host: "https://127.0.0.1:6443"},
		"http://loki.liquid-system.svc.cluster.local:3100",
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		"https://127.0.0.1:6443/api/v1/namespaces/liquid-system/services/http:loki:3100/proxy",
		access.endpoint,
	)
	require.NotNil(t, access.client.Transport)
}

func TestNewLogsAccessKeepsExternalEndpoint(t *testing.T) {
	access, err := newLogsAccess(&rest.Config{}, "https://logs.example/loki")
	require.NoError(t, err)
	assert.Equal(t, "https://logs.example/loki", access.endpoint)
}
