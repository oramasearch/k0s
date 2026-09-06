//go:build windows

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"errors"

	"github.com/spf13/cobra"
)

func newNodeAgentCmd() *cobra.Command {
	return unsupportedImmutableNodeCommand("node-agent")
}

func newBreakGlassSessionCmd() *cobra.Command {
	return unsupportedImmutableNodeCommand("break-glass-session")
}

func unsupportedImmutableNodeCommand(use string) *cobra.Command {
	return &cobra.Command{
		Use:    use,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("immutable node images are supported only on Linux")
		},
	}
}
