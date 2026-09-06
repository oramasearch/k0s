// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidadmission

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Decision is the subset of a liquid admission decision used for an Event.
type Decision struct {
	Sequence uint64 `json:"sequence"`
	Image    string `json:"image"`
	Outcome  string `json:"outcome"`
	Reasons  []struct {
		Message string `json:"message"`
	} `json:"reasons"`
}

// DecisionSink receives a namespace-qualified liquid decision.
type DecisionSink interface {
	Emit(context.Context, string, Decision) error
}

// ConsumeDecisions reads liquid's server-sent decision stream until it ends.
func ConsumeDecisions(ctx context.Context, client *http.Client, endpoint string, adapter *Adapter, sink DecisionSink) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create decision stream request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("open decision stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("decision stream returned %s", response.Status)
	}
	return scanDecisions(ctx, response.Body, adapter, sink)
}

func scanDecisions(ctx context.Context, reader io.Reader, adapter *Adapter, sink DecisionSink) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < len("data: ") || line[:len("data: ")] != "data: " {
			continue
		}
		var decision Decision
		if err := json.Unmarshal([]byte(line[len("data: "):]), &decision); err != nil {
			return fmt.Errorf("decode decision stream entry: %w", err)
		}
		namespace, found := adapter.NamespaceForSequence(decision.Sequence)
		if !found {
			continue
		}
		if err := sink.Emit(ctx, namespace, decision); err != nil {
			return fmt.Errorf("emit admission decision event: %w", err)
		}
	}
	return scanner.Err()
}
