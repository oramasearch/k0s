// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/stretchr/testify/require"
)

func TestLiquidChartsUseInternalRepository(t *testing.T) {
	liquid := v1beta1.DefaultLiquidSpec()
	for _, entry := range liquid.Components.Entries() {
		entry.Component.Chart.Repository = "https://charts.internal.example/liquid"
	}
	charts := liquidComponentCharts(liquid)
	require.Len(t, charts, 6)
	for _, chart := range charts {
		require.NotNil(t, chart.Repository)
		require.Equal(t, "https://charts.internal.example/liquid", chart.Repository.URL)
	}
	liquid.Components.Cilium.Replacement = v1beta1.LiquidReplacementExternal
	liquid.Components.GatewayAPI.Enabled = false
	charts = liquidComponentCharts(liquid)
	require.Len(t, charts, 5)
	require.True(t, charts[0].LeaveInstalled)
	require.Nil(t, charts[0].Repository)
	require.Empty(t, charts[0].ChartName)
}
