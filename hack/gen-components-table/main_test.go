// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratedComponentsPageIsCurrent(t *testing.T) {
	want, err := renderComponentsPage()
	require.NoError(t, err)

	got, err := os.ReadFile("../../docs/liquid/components.md")
	require.NoError(t, err)
	require.Equal(t, want, string(got), "run go run ./hack/gen-components-table")
}
