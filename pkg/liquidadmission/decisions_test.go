// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidadmission

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingDecisionSink struct {
	namespace string
	decision  Decision
}

func (s *recordingDecisionSink) Emit(_ context.Context, namespace string, decision Decision) error {
	s.namespace = namespace
	s.decision = decision
	return nil
}

func TestScanDecisionsEmitsMappedNamespace(t *testing.T) {
	adapter, err := NewAdapter("https://liquid.example", Routes{Version: 1, Default: "default", Namespaces: map[string]string{}}, nil)
	require.NoError(t, err)
	adapter.seen[42] = "tenant-a"
	sink := &recordingDecisionSink{}
	stream := strings.NewReader("event: decision\ndata: {\"sequence\":42,\"image\":\"registry.example/app@sha256:deadbeef\",\"outcome\":\"denied\",\"reasons\":[{\"message\":\"no attestation\"}]}\n\n")

	require.NoError(t, scanDecisions(context.Background(), stream, adapter, sink))
	assert.Equal(t, "tenant-a", sink.namespace)
	assert.Equal(t, uint64(42), sink.decision.Sequence)
	assert.Equal(t, "denied", sink.decision.Outcome)
	assert.Equal(t, "no attestation", sink.decision.Reasons[0].Message)
}
