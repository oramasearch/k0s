// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"strings"
	"testing"

	helmv1beta1 "github.com/k0sproject/k0s/pkg/apis/helm/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentRows(t *testing.T) {
	liquid := k0sv1beta1.DefaultLiquidSpec()
	liquid.Components.GatewayAPI.Replacement = k0sv1beta1.LiquidReplacementExternal
	liquid.Components.LocalPath.Enabled = false
	charts := []helmv1beta1.Chart{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "liquid-cilium"},
			Status: helmv1beta1.ChartStatus{
				Version: "1.20.1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "liquid-distributed-storage"},
			Status: helmv1beta1.ChartStatus{
				Version: "1.12.0",
			},
		},
	}

	rows := componentRows(liquid, charts)
	require.Len(t, rows, 6)
	assert.Equal(t, "deployed", rows[0].status)
	assert.Equal(t, "external", rows[1].status)
	assert.Equal(t, "pending", rows[2].status)
	assert.Equal(t, "pending", rows[3].status)
	assert.Equal(t, "disabled", rows[4].status)
	assert.Equal(t, "reconciling", rows[5].status)
	assert.Equal(t, "1.12.0", rows[5].deployed)
}

func TestPrintComponentRows(t *testing.T) {
	rows := []componentRow{{
		name:        "cilium",
		declared:    "1.20.1",
		deployed:    "1.20.1",
		replacement: "managed",
		status:      "deployed",
	}}
	var output strings.Builder

	require.NoError(t, printComponentRows(&output, rows))
	assert.Equal(t, "COMPONENT  DECLARED  DEPLOYED  REPLACEMENT  STATUS\ncilium     1.20.1    1.20.1    managed      deployed\n", output.String())
}

func TestPrintComponentRowsWithoutDeclarations(t *testing.T) {
	var output strings.Builder
	require.NoError(t, printComponentRows(&output, nil))
	assert.Equal(t, "No liquid components are declared.\n", output.String())
}
