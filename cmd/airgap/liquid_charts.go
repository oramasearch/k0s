// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"fmt"
	"runtime"

	"github.com/k0sproject/k0s/cmd/internal"
	"github.com/k0sproject/k0s/pkg/airgap"
	"github.com/k0sproject/k0s/pkg/config"
	imagespecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func newAirgapExportChartsCmd() *cobra.Command {
	var debugFlags internal.DebugFlags
	var directory, repositoryURL string
	env := airgap.TargetEnv{
		Platform: imagespecv1.Platform{OS: "linux", Architecture: runtime.GOARCH},
	}
	command := &cobra.Command{
		Use:              "export-charts",
		Short:            "Export managed liquid charts and print configuration for an internal repository",
		Args:             cobra.NoArgs,
		PersistentPreRun: debugFlags.Run,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := config.GetCmdOpts(command)
			if err != nil {
				return err
			}
			cluster, err := opts.K0sVars.NodeConfig()
			if err != nil {
				return fmt.Errorf("failed to get node config: %w", err)
			}
			env.Spec = cluster.Spec
			env.Context = command.Context()
			spec, err := airgap.ExportLiquidCharts(env, directory, repositoryURL)
			if err != nil {
				return err
			}
			cluster = cluster.DeepCopy()
			cluster.Spec = spec
			data, err := yaml.Marshal(cluster)
			if err != nil {
				return err
			}
			_, err = command.OutOrStdout().Write(data)
			return err
		},
	}
	debugFlags.AddToFlagSet(command.PersistentFlags())
	flags := command.Flags()
	flags.AddFlagSet(config.GetPersistentFlagSet())
	flags.AddFlagSet(config.FileInputFlag())
	flags.Var((*platformFlag)(&env.Platform), "platform", "the Linux worker platform")
	flags.StringVar(&env.ChartDir, "chart-dir", "", "read exact chart archives from this directory without network access")
	flags.StringVar(&directory, "output", "liquid-charts", "new directory for archives, index.yaml and charts.lock.json")
	flags.StringVar(&repositoryURL, "repository", "", "internal HTTPS Helm repository URL reachable before cluster startup")
	if err := command.MarkFlagRequired("repository"); err != nil {
		panic(err)
	}
	return command
}
