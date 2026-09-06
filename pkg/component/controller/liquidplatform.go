// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"os"
	"path/filepath"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/static"
)

// LiquidPlatform writes the cluster-wide liquid platform resources for the manifest applier.
type LiquidPlatform struct {
	manifestDir string
}

var _ manager.Component = (*LiquidPlatform)(nil)
var _ manager.Reconciler = (*LiquidPlatform)(nil)

// NewLiquidPlatform creates the liquid platform manifest component.
func NewLiquidPlatform(manifestsDir string) *LiquidPlatform {
	return &LiquidPlatform{
		manifestDir: filepath.Join(manifestsDir, "liquid-platform"),
	}
}

// Init creates the manifest directory.
func (l *LiquidPlatform) Init(context.Context) error {
	return dir.Init(l.manifestDir, constant.ManifestsDirMode)
}

// Start starts the component.
func (l *LiquidPlatform) Start(context.Context) error {
	return nil
}

// Reconcile renders platform resources from the current cluster configuration.
func (l *LiquidPlatform) Reconcile(_ context.Context, clusterConfig *v1beta1.ClusterConfig) error {
	if clusterConfig.Spec == nil || clusterConfig.Spec.Liquid == nil {
		return os.RemoveAll(l.manifestDir)
	}

	manifest, err := static.RenderLiquidPlatform(clusterConfig.Spec.Liquid.Platform)
	if err != nil {
		return err
	}
	if err := dir.Init(l.manifestDir, constant.ManifestsDirMode); err != nil {
		return err
	}
	return file.AtomicWithTarget(filepath.Join(l.manifestDir, "platform.yaml")).
		Write(manifest)
}

// Stop stops the component.
func (l *LiquidPlatform) Stop() error {
	return nil
}
