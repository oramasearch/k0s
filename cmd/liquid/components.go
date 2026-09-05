// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"fmt"
	"io"
	"text/tabwriter"

	helmv1beta1 "github.com/k0sproject/k0s/pkg/apis/helm/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/client/clientset"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spf13/cobra"
)

type componentRow struct {
	name        string
	declared    string
	deployed    string
	replacement string
	status      string
}

func newComponentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "components",
		Short: "Print declared and deployed liquid component versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := config.GetCmdOpts(cmd)
			if err != nil {
				return err
			}
			restConfig, err := kubernetes.ClientConfig(kubernetes.KubeconfigFromFile(opts.K0sVars.AdminKubeConfigPath))
			if err != nil {
				return fmt.Errorf("can't load the admin kubeconfig: %w", err)
			}
			clients, err := clientset.NewForConfig(restConfig)
			if err != nil {
				return fmt.Errorf("can't create the Kubernetes client: %w", err)
			}

			clusterConfig, err := clients.K0sV1beta1().ClusterConfigs(constant.ClusterConfigNamespace).Get(
				cmd.Context(),
				constant.ClusterConfigObjectName,
				metav1.GetOptions{},
			)
			if err != nil {
				return fmt.Errorf("can't read the cluster configuration: %w", err)
			}
			charts, err := clients.HelmV1beta1().Charts(metav1.NamespaceSystem).List(cmd.Context(), metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("can't list Helm charts: %w", err)
			}

			var liquid *k0sv1beta1.LiquidSpec
			if clusterConfig.Spec != nil {
				liquid = clusterConfig.Spec.Liquid
			}
			rows := componentRows(liquid, charts.Items)
			return printComponentRows(cmd.OutOrStdout(), rows)
		},
	}

	cmd.Flags().AddFlagSet(config.GetPersistentFlagSet())
	return cmd
}

func componentRows(liquid *k0sv1beta1.LiquidSpec, charts []helmv1beta1.Chart) []componentRow {
	if liquid == nil || liquid.Components == nil {
		return nil
	}

	chartsByName := make(map[string]helmv1beta1.Chart, len(charts))
	for _, chart := range charts {
		chartsByName[chart.Name] = chart
	}

	rows := make([]componentRow, 0, len(liquid.Components.Entries()))
	for _, entry := range liquid.Components.Entries() {
		component := entry.Component
		row := componentRow{
			name:        entry.Name,
			declared:    component.Version,
			deployed:    "-",
			replacement: component.Replacement,
			status:      "pending",
		}
		switch {
		case !component.Enabled:
			row.status = "disabled"
		case component.Replacement == k0sv1beta1.LiquidReplacementExternal:
			row.status = "external"
		default:
			chartName := k0sv1beta1.LiquidComponentChartResourceName(entry.Name)
			chart, found := chartsByName[chartName]
			if found {
				row.deployed = chart.Status.Version
				if row.deployed == "" {
					row.deployed = "-"
				}
				switch {
				case chart.Status.Error != "":
					row.status = "error"
				case chart.Status.Version == component.Version:
					row.status = "deployed"
				default:
					row.status = "reconciling"
				}
			}
		}
		rows = append(rows, row)
	}

	return rows
}

func printComponentRows(output io.Writer, rows []componentRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(output, "No liquid components are declared.")
		return err
	}

	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "COMPONENT\tDECLARED\tDEPLOYED\tREPLACEMENT\tSTATUS"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", row.name, row.declared, row.deployed, row.replacement, row.status); err != nil {
			return err
		}
	}

	return writer.Flush()
}
