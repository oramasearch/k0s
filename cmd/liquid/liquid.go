// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// NewLiquidCmd creates the liquid-specific command group.
func NewLiquidCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "liquid",
		Short: "Inspect liquid cluster features",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return pflag.ErrHelp
		},
	}

	cmd.AddCommand(newComponentsCmd())
	cmd.AddCommand(newLogsCmd())
	cmd.AddCommand(newNodeImageCmd())
	cmd.AddCommand(newNodeAgentCmd())
	cmd.AddCommand(newBreakGlassSessionCmd())
	cmd.AddCommand(newAdmissionAdapterCmd())
	cmd.AddCommand(newTenantProfileCmd())
	cmd.AddCommand(newUntrustedRuntimeClassCmd())
	return cmd
}
