// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantProfileCommand(t *testing.T) {
	var output strings.Builder
	command := newTenantProfileCmd()
	command.SetOut(&output)
	command.SetArgs([]string{"preview-change-21"})

	require.NoError(t, command.Execute())
	assert.Contains(t, output.String(), "name: preview-change-21")
	assert.Contains(t, output.String(), "pod-security.kubernetes.io/enforce: restricted")
}

func TestUntrustedRuntimeClassCommand(t *testing.T) {
	var output strings.Builder
	command := newUntrustedRuntimeClassCmd()
	command.SetOut(&output)

	require.NoError(t, command.Execute())
	assert.Contains(t, output.String(), "name: untrusted")
	assert.Contains(t, output.String(), "handler: kata")
}
