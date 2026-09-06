// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidsnapshotter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/contrib/snapshotservice"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/component/manager"
	workercontainerd "github.com/k0sproject/k0s/pkg/component/worker/containerd"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
)

// Component serves the containerd proxy snapshotter socket.
type Component struct {
	K0sVars *config.CfgVars

	socketPath    string
	nodeStorePath string
	runtime       *containerdRuntime
	snapshotter   *Snapshotter
	server        *grpc.Server
	listener      net.Listener
	serveErr      chan error
	stopOnce      sync.Once
}

var _ manager.Component = (*Component)(nil)
var _ manager.Ready = (*Component)(nil)

// NewComponent creates the liquid proxy snapshotter worker component.
func NewComponent(vars *config.CfgVars) *Component {
	return &Component{
		K0sVars:       vars,
		socketPath:    filepath.Join(vars.RunDir, "liquid-snapshotter.sock"),
		nodeStorePath: filepath.Join(vars.RunDir, "liquid.sock"),
		serveErr:      make(chan error, 1),
	}
}

// Init creates the snapshotter socket directory.
func (c *Component) Init(context.Context) error {
	return dir.Init(filepath.Dir(c.socketPath), constant.RunDirMode)
}

// Start binds the proxy socket before containerd starts.
func (c *Component) Start(context.Context) error {
	if err := removeStaleSocket(c.socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("listen on liquid snapshotter socket: %w", err)
	}
	if err := os.Chmod(c.socketPath, 0600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("set liquid snapshotter socket permissions: %w", err)
	}

	c.runtime = &containerdRuntime{address: workercontainerd.Address(c.K0sVars.RunDir)}
	c.snapshotter = New(c.runtime.snapshotter, c.runtime, newNodeStoreHTTPClient(c.nodeStorePath))
	c.server = grpc.NewServer()
	snapshotsapi.RegisterSnapshotsServer(c.server, snapshotservice.FromSnapshotter(c.snapshotter))
	c.listener = listener

	go func() {
		logrus.WithField("component", "liquid-snapshotter").Info("Serving liquid snapshotter")
		if err := c.server.Serve(listener); err != nil {
			c.serveErr <- err
		}
	}()
	return nil
}

// Ready reports whether the snapshotter socket remains available.
func (c *Component) Ready() error {
	if c.listener == nil {
		return errors.New("liquid snapshotter is not listening")
	}
	select {
	case err := <-c.serveErr:
		return fmt.Errorf("liquid snapshotter server stopped: %w", err)
	default:
		return nil
	}
}

// Stop stops the gRPC server and releases node-store mounts.
func (c *Component) Stop() error {
	var stopErr error
	c.stopOnce.Do(func() {
		if c.server != nil {
			c.server.Stop()
		}
		if c.listener != nil {
			if err := c.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				stopErr = errors.Join(stopErr, err)
			}
		}
		if c.snapshotter != nil {
			stopErr = errors.Join(stopErr, c.snapshotter.Close())
		}
		if c.runtime != nil {
			stopErr = errors.Join(stopErr, c.runtime.Close())
		}
		if err := os.Remove(c.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			stopErr = errors.Join(stopErr, err)
		}
	})
	return stopErr
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	return os.Remove(path)
}
