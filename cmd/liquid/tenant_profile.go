// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"github.com/k0sproject/k0s/static"

	"github.com/spf13/cobra"
)

func newTenantProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tenant-profile NAMESPACE",
		Short: "Render the baseline resources for a tenant namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, err := static.RenderTenantNamespaceProfile(args[0])
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(profile)
			return err
		},
	}
}

func newUntrustedRuntimeClassCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "untrusted-runtime-class",
		Short: "Print the liquid Kata RuntimeClass declaration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(static.UntrustedRuntimeClass())
			return err
		},
	}
}
