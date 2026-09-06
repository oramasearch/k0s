//go:build unix

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"fmt"
	"os"
	"time"

	"github.com/k0sproject/k0s/pkg/component/controller/nodeimage"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func newNodeAgentCmd() *cobra.Command {
	var nodeName string
	var kubeconfigPath string
	var root string
	var interval time.Duration
	command := &cobra.Command{
		Use:    "node-agent",
		Short:  "Apply immutable-node host instructions",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			configuration, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
			if err != nil {
				return fmt.Errorf("load node-agent kubeconfig: %w", err)
			}
			client, err := kubernetes.NewForConfig(configuration)
			if err != nil {
				return fmt.Errorf("create node-agent client: %w", err)
			}
			return nodeimage.RunAgent(command.Context(), nodeimage.AgentOptions{
				NodeName: nodeName,
				Root:     root,
				Interval: interval,
				Client:   client,
			})
		},
	}
	command.Flags().StringVar(&nodeName, "node-name", "", "Kubernetes node name")
	command.Flags().StringVar(&kubeconfigPath, "kubeconfig", "/var/lib/k0s/kubelet.conf", "node kubeconfig")
	command.Flags().StringVar(&root, "root", "/", "host root")
	command.Flags().DurationVar(&interval, "interval", 2*time.Second, "instruction polling interval")
	return command
}

func newBreakGlassSessionCmd() *cobra.Command {
	var nodeName string
	var root string
	command := &cobra.Command{
		Use:    "break-glass-session",
		Short:  "Run one audited break-glass shell",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if nodeName == "" {
				detected, err := os.Hostname()
				if err != nil {
					return err
				}
				nodeName = detected
			}
			return nodeimage.RunBreakGlassSession(command.Context(), root, nodeName)
		},
	}
	command.Flags().StringVar(&nodeName, "node-name", "", "Kubernetes node name")
	command.Flags().StringVar(&root, "root", "/", "host root")
	return command
}
