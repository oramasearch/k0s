// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidsnapshotter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

type nodeStore interface {
	Mount(context.Context, string, string) (string, error)
	Unmount(context.Context, string) error
}

type nodeStoreHTTPClient struct {
	client *http.Client
}

func newNodeStoreHTTPClient(socketPath string) *nodeStoreHTTPClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &nodeStoreHTTPClient{
		client: &http.Client{Transport: transport},
	}
}

func (c *nodeStoreHTTPClient) Mount(ctx context.Context, tree, storeURL string) (string, error) {
	body := struct {
		Tree     string `json:"tree"`
		StoreURL string `json:"store_url"`
		Bearer   string `json:"bearer"`
	}{
		Tree:     tree,
		StoreURL: storeURL,
	}

	var result struct {
		Path string `json:"path"`
	}
	if err := c.post(ctx, "/mount", body, &result); err != nil {
		return "", err
	}
	if result.Path == "" {
		return "", errors.New("liquid node store returned an empty mount path")
	}
	return result.Path, nil
}

func (c *nodeStoreHTTPClient) Unmount(ctx context.Context, tree string) error {
	body := struct {
		Tree string `json:"tree"`
	}{Tree: tree}
	return c.post(ctx, "/unmount", body, nil)
}

func (c *nodeStoreHTTPClient) post(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://liquid"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("request liquid node store: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read liquid node-store response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("liquid node store returned %s: %s", response.Status, responseBody)
	}
	if output == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode liquid node-store response: %w", err)
	}
	return nil
}
