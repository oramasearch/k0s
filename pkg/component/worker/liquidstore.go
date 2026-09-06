// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/supervisor"
)

const liquidNodeStoreControlAddress = "127.0.0.1:8082"

// LiquidStore manages the per-node liquid chunk store.
type LiquidStore struct {
	K0sVars *config.CfgVars

	executablePath string
	rootPath       string
	socketPath     string
	supervisor     *supervisor.Supervisor
}

var _ manager.Component = (*LiquidStore)(nil)
var _ manager.Ready = (*LiquidStore)(nil)

// NewLiquidStore creates the worker component for liquid node-store.
func NewLiquidStore(vars *config.CfgVars) *LiquidStore {
	return &LiquidStore{
		K0sVars:    vars,
		rootPath:   filepath.Join(vars.DataDir, "liquid"),
		socketPath: filepath.Join(vars.RunDir, "liquid.sock"),
	}
}

// Init stages liquid and creates its data directory.
func (s *LiquidStore) Init(context.Context) error {
	var err error
	s.executablePath, err = assets.StageExecutable(s.K0sVars.BinDir, "liquid")
	if err != nil {
		return fmt.Errorf("failed to stage liquid: %w", err)
	}
	if err := dir.Init(s.rootPath, constant.DataDirMode); err != nil {
		return fmt.Errorf("failed to create liquid node-store root: %w", err)
	}
	return dir.Init(filepath.Dir(s.socketPath), constant.RunDirMode)
}

// Start starts liquid node-store in the worker mount namespace.
func (s *LiquidStore) Start(ctx context.Context) error {
	logrus.WithField("component", "liquid-node-store").Info("Starting liquid node store")
	s.supervisor = &supervisor.Supervisor{
		Name:    "liquid-node-store",
		BinPath: s.executablePath,
		RunDir:  s.K0sVars.RunDir,
		DataDir: s.K0sVars.DataDir,
		Args: []string{
			"node-store",
			"serve",
			"--root",
			s.rootPath,
			"--listen",
			"unix://" + s.socketPath,
			"--control-listen",
			liquidNodeStoreControlAddress,
		},
	}
	return s.supervisor.Supervise(ctx)
}

// Ready checks the node-store control endpoint.
func (s *LiquidStore) Ready() error {
	client := http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + liquidNodeStoreControlAddress + "/readyz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("liquid node store returned %s", response.Status)
	}
	return nil
}

// Stop stops liquid node-store.
func (s *LiquidStore) Stop() error {
	if s.supervisor == nil {
		return nil
	}
	return s.supervisor.Stop()
}
