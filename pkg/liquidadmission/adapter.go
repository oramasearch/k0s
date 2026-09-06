// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidadmission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sync"
)

// Adapter forwards AdmissionReviews to the selected liquid policy set.
type Adapter struct {
	liquid *url.URL
	routes Routes
	client *http.Client
	mu     sync.Mutex
	seen   map[uint64]string
}

// NewAdapter constructs an AdmissionReview proxy for one liquid service.
func NewAdapter(liquidURL string, routes Routes, client *http.Client) (*Adapter, error) {
	parsed, err := url.Parse(liquidURL)
	if err != nil {
		return nil, fmt.Errorf("parse liquid URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("liquid URL must be HTTPS with a host")
	}
	if routes.Default == "" {
		return nil, errors.New("default liquid policy is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Adapter{liquid: parsed, routes: routes, client: client, seen: make(map[uint64]string)}, nil
}

// ServeHTTP selects the namespace policy and forwards the unchanged review.
func (a *Adapter) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(response, "read AdmissionReview", http.StatusBadRequest)
		return
	}
	namespace, err := admissionNamespace(body)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	policy := a.routes.Default
	if mapped, found := a.routes.Namespaces[namespace]; found {
		policy = mapped
	}
	a.mu.Unlock()
	target := *a.liquid
	target.Path = path.Join(target.Path, "admit", policy)
	forward, err := http.NewRequestWithContext(request.Context(), http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(response, "create liquid request", http.StatusInternalServerError)
		return
	}
	forward.Header.Set("Content-Type", "application/json")
	forwardResponse, err := a.client.Do(forward)
	if err != nil {
		http.Error(response, "liquid admission unavailable", http.StatusServiceUnavailable)
		return
	}
	defer forwardResponse.Body.Close()
	responseBody, err := io.ReadAll(forwardResponse.Body)
	if err != nil {
		http.Error(response, "read liquid AdmissionReview", http.StatusBadGateway)
		return
	}
	a.recordNamespace(namespace, responseBody)
	response.Header().Set("Content-Type", forwardResponse.Header.Get("Content-Type"))
	response.WriteHeader(forwardResponse.StatusCode)
	_, _ = response.Write(responseBody)
}

// NamespaceForSequence returns the namespace remembered from an AdmissionReview.
func (a *Adapter) NamespaceForSequence(sequence uint64) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	namespace, found := a.seen[sequence]
	return namespace, found
}

// ReplaceRoutes atomically changes the namespace-to-policy mapping.
func (a *Adapter) ReplaceRoutes(routes Routes) error {
	if routes.Default == "" {
		return errors.New("default liquid policy is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routes = routes
	return nil
}

func (a *Adapter) recordNamespace(namespace string, response []byte) {
	var review struct {
		Response struct {
			Status struct {
				Decisions []Decision `json:"decisions"`
			} `json:"status"`
		} `json:"response"`
	}
	if json.Unmarshal(response, &review) != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, decision := range review.Response.Status.Decisions {
		a.seen[decision.Sequence] = namespace
	}
}

func admissionNamespace(body []byte) (string, error) {
	var review struct {
		Request struct {
			Namespace string `json:"namespace"`
			Object    struct {
				Metadata struct {
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"request"`
	}
	if err := json.Unmarshal(body, &review); err != nil {
		return "", fmt.Errorf("decode AdmissionReview: %w", err)
	}
	if review.Request.Namespace != "" {
		return review.Request.Namespace, nil
	}
	if review.Request.Object.Metadata.Namespace != "" {
		return review.Request.Object.Metadata.Namespace, nil
	}
	return "", errors.New("AdmissionReview has no Pod namespace")
}
