// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidcomponents

import (
	"context"
	"testing"
	"time"

	helmv1beta1 "github.com/k0sproject/k0s/pkg/apis/helm/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/client/clientset"
	"github.com/k0sproject/k0s/pkg/constant"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/stretchr/testify/suite"
)

const bumpedLocalPathVersion = "0.0.37"

type LiquidComponentsSuite struct {
	common.BootlooseSuite
}

type chartSnapshot struct {
	version         string
	resourceVersion string
}

func (s *LiquidComponentsSuite) TestDeclaredVersionsSurviveBinaryUpgrade() {
	controller := s.ControllerNode(0)
	_, err := s.RunCommandController(0, "/usr/local/bin/k0s config create > /tmp/k0s.yaml")
	s.Require().NoError(err)
	s.Require().NoError(s.InitController(0, "--enable-dynamic-config", "--config=/tmp/k0s.yaml"))
	token, err := s.GetJoinToken("worker")
	s.Require().NoError(err)
	s.Require().NoError(s.RunWorkersWithToken(token))

	restConfig, err := s.GetKubeConfig(controller)
	s.Require().NoError(err)
	clients, err := clientset.NewForConfig(restConfig)
	s.Require().NoError(err)

	declared := defaultVersions()
	beforeUpgrade := s.waitForChartVersions(clients, declared)
	s.logComponents()

	s.Require().NoError(s.StopController(controller))
	_, err = s.RunCommandController(0, "install -m 0755 /dist/k0s-new /usr/local/bin/k0s")
	s.Require().NoError(err)
	s.Require().NoError(s.StartController(controller))
	s.Require().NoError(s.WaitForKubeAPI(controller, ""))

	afterUpgrade := s.waitForChartVersions(clients, declared)
	s.Equal(beforeUpgrade, afterUpgrade, "binary replacement changed a declared component Chart")
	s.logComponents()

	configs := clients.K0sV1beta1().ClusterConfigs(constant.ClusterConfigNamespace)
	clusterConfig, err := configs.Get(s.Context(), constant.ClusterConfigObjectName, metav1.GetOptions{})
	s.Require().NoError(err)
	clusterConfig.Spec.Liquid.Components.LocalPath.Version = bumpedLocalPathVersion
	_, err = configs.Update(s.Context(), clusterConfig, metav1.UpdateOptions{FieldManager: s.T().Name()})
	s.Require().NoError(err)

	declared[k0sv1beta1.LiquidComponentLocalPath] = bumpedLocalPathVersion
	afterVersionChange := s.waitForChartVersions(clients, declared)
	for componentName, original := range afterUpgrade {
		changed := afterVersionChange[componentName]
		if componentName == k0sv1beta1.LiquidComponentLocalPath {
			s.NotEqual(original.resourceVersion, changed.resourceVersion)
			s.Equal(bumpedLocalPathVersion, changed.version)
			continue
		}
		s.Equal(original, changed, componentName)
	}
	s.logComponents()
}

func defaultVersions() map[string]string {
	versions := make(map[string]string)
	for _, entry := range k0sv1beta1.DefaultLiquidSpec().Components.Entries() {
		versions[entry.Name] = entry.Component.Version
	}
	return versions
}

func (s *LiquidComponentsSuite) waitForChartVersions(clients clientset.Interface, expected map[string]string) map[string]chartSnapshot {
	var result map[string]chartSnapshot
	err := wait.PollUntilContextTimeout(s.Context(), time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		charts, err := clients.HelmV1beta1().Charts(metav1.NamespaceSystem).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil
		}

		byName := make(map[string]helmv1beta1.Chart, len(charts.Items))
		for _, chart := range charts.Items {
			byName[chart.Name] = chart
		}
		observed := make(map[string]chartSnapshot, len(expected))
		for componentName, version := range expected {
			resourceName := k0sv1beta1.LiquidComponentChartResourceName(componentName)
			chart, found := byName[resourceName]
			if !found || chart.Spec.Version != version || chart.Status.Version != version {
				return false, nil
			}
			observed[componentName] = chartSnapshot{
				version:         chart.Status.Version,
				resourceVersion: chart.ResourceVersion,
			}
		}
		result = observed
		return true, nil
	})
	s.Require().NoError(err)
	return result
}

func (s *LiquidComponentsSuite) logComponents() {
	output, err := s.RunCommandController(0, s.K0sFullPath+" liquid components")
	s.Require().NoError(err)
	s.T().Log("liquid component versions:\n" + output)
}

func TestLiquidComponentsSuite(t *testing.T) {
	s := LiquidComponentsSuite{
		BootlooseSuite: common.BootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     2,
		},
	}
	suite.Run(t, &s)
}
