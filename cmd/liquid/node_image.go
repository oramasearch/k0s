// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"fmt"
	"io"
	"text/tabwriter"

	liquidv1alpha1 "github.com/k0sproject/k0s/pkg/apis/liquid/v1alpha1"
	"github.com/k0sproject/k0s/pkg/client/clientset"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newNodeImageCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "node-image",
		Short: "Inspect immutable node-image plans",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return pflag.ErrHelp
		},
	}
	command.AddCommand(newNodeImageStatusCmd())
	return command
}

func newNodeImageStatusCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "status [NAME]",
		Short: "Print immutable node-image rollout status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			options, err := config.GetCmdOpts(command)
			if err != nil {
				return err
			}
			restConfig, err := kubernetes.ClientConfig(kubernetes.KubeconfigFromFile(options.K0sVars.AdminKubeConfigPath))
			if err != nil {
				return fmt.Errorf("load admin kubeconfig: %w", err)
			}
			clients, err := clientset.NewForConfig(restConfig)
			if err != nil {
				return fmt.Errorf("create liquid client: %w", err)
			}
			var plans []liquidv1alpha1.NodeImage
			if len(args) == 1 {
				plan, err := clients.LiquidV1alpha1().NodeImages().Get(command.Context(), args[0], metav1.GetOptions{})
				if err != nil {
					return err
				}
				plans = []liquidv1alpha1.NodeImage{*plan}
			} else {
				list, err := clients.LiquidV1alpha1().NodeImages().List(command.Context(), metav1.ListOptions{})
				if err != nil {
					return err
				}
				plans = list.Items
			}
			return printNodeImageStatus(command.OutOrStdout(), plans)
		},
	}
	command.Flags().AddFlagSet(config.GetPersistentFlagSet())
	return command
}

func printNodeImageStatus(output io.Writer, plans []liquidv1alpha1.NodeImage) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "PLAN\tDIGEST\tPHASE\tNODE\tROLE\tSTATE\tMESSAGE"); err != nil {
		return err
	}
	for _, plan := range plans {
		if len(plan.Status.Targets) == 0 {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t-\t-\t-\t-\n", plan.Name, plan.Spec.Digest, plan.Status.Phase); err != nil {
				return err
			}
			continue
		}
		for _, target := range plan.Status.Targets {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", plan.Name, plan.Status.ObservedDigest, plan.Status.Phase, target.Name, target.Role, target.State, target.Message); err != nil {
				return err
			}
		}
	}
	return writer.Flush()
}
