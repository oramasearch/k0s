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

func TestAdapterForwardsTransparencyVerificationDecisions(t *testing.T) {
	const accepted = `{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","response":{"allowed":true,"status":{"decisions":[{"sequence":17,"outcome":"allowed","evidence":[{"log":{"outcome":"included","checkpoint":"sha256:1234","checkpoint_sequence":9,"tree_size":3,"reason":"included in verified log"}}]}]}}}`
	const forgedTile = `{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","response":{"allowed":false,"status":{"decisions":[{"sequence":18,"outcome":"denied","reasons":[{"message":"tile level=1 index=0 width=3 does not match its parent"}],"evidence":[{"log":{"outcome":"not_included","reason":"tile level=1 index=0 width=3 does not match its parent"}}]}]}}}`

	liquid := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/admit/default" {
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(accepted))
			return
		}
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(forgedTile))
	}))
	defer liquid.Close()

	adapter, err := NewAdapter(liquid.URL, Routes{
		Version: 1,
		Default: "default",
		Namespaces: map[string]string{
			"forged": "forged--verify",
		},
	}, liquid.Client())
	require.NoError(t, err)

	acceptedRequest := httptest.NewRequest(http.MethodPost, "/admission/review", strings.NewReader(`{"request":{"namespace":"verified"}}`))
	acceptedResponse := httptest.NewRecorder()
	adapter.ServeHTTP(acceptedResponse, acceptedRequest)

	assert.Equal(t, http.StatusOK, acceptedResponse.Code)
	assert.JSONEq(t, accepted, acceptedResponse.Body.String())
	assert.Equal(t, "verified", namespaceForSequence(t, adapter, 17))

	forgedRequest := httptest.NewRequest(http.MethodPost, "/admission/review", strings.NewReader(`{"request":{"namespace":"forged"}}`))
	forgedResponse := httptest.NewRecorder()
	adapter.ServeHTTP(forgedResponse, forgedRequest)

	assert.Equal(t, http.StatusForbidden, forgedResponse.Code)
	assert.JSONEq(t, forgedTile, forgedResponse.Body.String())
	assert.Contains(t, forgedResponse.Body.String(), "tile level=1 index=0 width=3")
	assert.Equal(t, "forged", namespaceForSequence(t, adapter, 18))
}

func namespaceForSequence(t *testing.T, adapter *Adapter, sequence uint64) string {
	t.Helper()
	namespace, found := adapter.NamespaceForSequence(sequence)
	require.True(t, found)
	return namespace
}
