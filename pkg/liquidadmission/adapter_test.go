// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidadmission

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdapterRoutesAdmissionReviewByNamespace(t *testing.T) {
	var receivedPath string
	liquid := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedPath = request.URL.Path
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"allowed":true}`))
	}))
	defer liquid.Close()

	adapter, err := NewAdapter(liquid.URL, Routes{
		Version: 1,
		Default: "default",
		Namespaces: map[string]string{
			"audit": "audit--audit-only",
		},
	}, liquid.Client())
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodPost, "/admission/review", io.NopCloser(strings.NewReader(`{"request":{"namespace":"audit"}}`)))
	response := httptest.NewRecorder()
	adapter.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "/admit/audit--audit-only", receivedPath)
}

func TestAdapterRejectsReviewWithoutNamespace(t *testing.T) {
	adapter, err := NewAdapter("https://liquid.example", Routes{Version: 1, Default: "default"}, http.DefaultClient)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/admission/review", strings.NewReader(`{"request":{}}`))
	response := httptest.NewRecorder()
	adapter.ServeHTTP(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}
