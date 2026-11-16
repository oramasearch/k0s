// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package cplb

import (
	"os/exec"
	"testing"

	k0sAPI "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEscapeSingleQuotes(t *testing.T) {
	assert.Equal(t, `It\'s a "nice" \'test\'`, escapeSingleQuotes(`It's a "nice" \'test'`))
}

func TestKeepalivedInit_NotAvailable(t *testing.T) {
	// The test executable has no embedded binaries, just like a k0s build
	// without an embedded keepalived executable.
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()

	underTest := Keepalived{
		K0sVars: &config.CfgVars{BinDir: dir, RunDir: dir},
		Config:  &k0sAPI.KeepalivedSpec{},
	}

	err := underTest.Init(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrNotFound)
	assert.ErrorContains(t, err, "control plane load balancing requires keepalived, which is neither embedded in k0s nor installed on this host")
}
