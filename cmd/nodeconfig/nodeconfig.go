//go:build unix

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeconfig

import (
	"fmt"
	"os"

	configfile "github.com/k0sproject/k0s/pkg/nodeconfig"
	"github.com/spf13/cobra"
)

// NewNodeConfigCmd creates the signed first-boot configuration command.
func NewNodeConfigCmd() *cobra.Command {
	var signingKeyPath string
	var root string
	command := &cobra.Command{
		Use:   "node-config FILE",
		Short: "Apply a signed immutable-node machine configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read node configuration: %w", err)
			}
			signingKey, err := os.ReadFile(signingKeyPath)
			if err != nil {
				return fmt.Errorf("read node-config signing key: %w", err)
			}
			configuration, err := configfile.ParseAndVerify(data, signingKey)
			if err != nil {
				return err
			}
			return configuration.Apply(root, data)
		},
	}
	command.Flags().StringVar(&signingKeyPath, "signing-key", "/usr/share/k0s/node-config-signing.pub", "trusted Ed25519 node-config signing key")
	command.Flags().StringVar(&root, "root", "/", "host root to configure")
	return command
}
