// SPDX-FileCopyrightText: 2023 k0s authors
// SPDX-License-Identifier: Apache-2.0

package containerd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml"

	"github.com/k0sproject/k0s/internal/pkg/file"
)

const liquidDropInHeader = "# k0s_managed=true\n"

// LiquidDropInPath returns the k0s-managed liquid snapshotter drop-in path.
func LiquidDropInPath(importsPath string) string {
	return filepath.Join(importsPath, "liquid.toml")
}

func reconcileLiquidDropIn(importsPath, socketPath string) error {
	path := LiquidDropInPath(importsPath)
	if socketPath == "" {
		return removeLiquidDropIn(path)
	}

	data, err := marshalLiquidDropIn(socketPath)
	if err != nil {
		return err
	}

	return file.AtomicWithTarget(path).
		WithPermissions(0644).
		Do(func(dst file.AtomicWriter) error {
			_, err := dst.Write(data)
			return err
		})
}

func removeLiquidDropIn(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, []byte(liquidDropInHeader)) {
		return nil
	}
	return os.Remove(path)
}

func marshalLiquidDropIn(socketPath string) ([]byte, error) {
	data, err := toml.Marshal(map[string]any{
		"version": 3,
		"proxy_plugins": map[string]any{
			"liquid": map[string]any{
				"type":    "snapshot",
				"address": socketPath,
			},
		},
		"plugins": map[string]any{
			"io.containerd.cri.v1.images": map[string]any{
				"snapshotter": "liquid",
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("can't marshal liquid containerd drop-in: %w", err)
	}
	return append([]byte(liquidDropInHeader), data...), nil
}

// marshalContainerdConfig returns the TOML bytes for the k0s-managed containerd
// configuration, with CRI plugin defaults inline and a glob import for user drop-ins.
func marshalContainerdConfig(importsPath, sandboxImage string) ([]byte, error) {
	imagesConf := map[string]any{
		"pinned_images": map[string]any{
			"sandbox": sandboxImage,
		},
	}

	runtimeConf := map[string]any{}

	if runtime.GOOS == "windows" {
		runtimeConf["cni"] = map[string]any{
			"conf_dir": `c:\etc\cni\net.d`,
			"bin_dirs": []any{`c:\opt\cni\bin`},
		}
	}

	return toml.Marshal(map[string]any{
		"version": 3,
		"imports": []string{filepath.Join(importsPath, "*.toml")},
		"plugins": map[string]any{
			"io.containerd.cri.v1.images":  imagesConf,
			"io.containerd.cri.v1.runtime": runtimeConf,
		},
	})
}

// ValidateConfigFile checks if the given config data is valid and compatible with the k0s-managed
// version of containerd. Returns a descriptive error if not.
func ValidateConfigFile(data []byte) error {
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return fmt.Errorf("failed to parse TOML: %w", err)
	}
	return validateConfigTree(tree)
}

// validateConfigTree performs compatibility checks on a parsed containerd
// configuration tree and returns a combined error if any incompatibilities are found.
func validateConfigTree(tree *toml.Tree) error {
	var errs []error

	if err := assertV3Config(tree); err != nil {
		errs = append(errs, err)
	}

	if hasV2CRIConfig(tree) {
		errs = append(errs, errors.New("configuration contains a [plugins.\"io.containerd.grpc.v1.cri\"] section which is the containerd v1 CRI plugin format"))
	}

	return errors.Join(errs...)
}

// hasV2CRIConfig checks if the given containerd configuration tree contains any plugins."io.containerd.grpc.v1.cri"
// configuration section. This is the main difference between containerd v2 and v3 configuration formats, so if this is present we
// want the user to fix it manually instead of trying to merge it and potentially causing unexpected breakage.
func hasV2CRIConfig(tree *toml.Tree) bool {
	return tree.HasPath([]string{"plugins", "io.containerd.grpc.v1.cri"})
}

func assertV3Config(tree *toml.Tree) error {
	version := tree.Get("version")
	if version != nil {
		v, ok := version.(int64)
		if !ok {
			return fmt.Errorf("unexpected type for version field: expected int64, got %T", version)
		}

		if v != 3 {
			return fmt.Errorf("unsupported configuration version: expected 3, got %d", v)
		}
	}
	return nil
}
