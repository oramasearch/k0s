// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iptables

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/sirupsen/logrus"
)

const (
	ModeNFT    = "nft"
	ModeLegacy = "legacy"
)

type Component struct {
	IPTablesMode string
	BinDir       string
}

func (c *Component) Init(_ context.Context) error {
	log := logrus.WithField("component", constant.IptablesBinariesComponentName)
	log.Info("Staging iptables binaries")
	iptablesMode, err := extractIPTablesBinaries(c.BinDir, c.IPTablesMode)
	if err != nil {
		return err
	}

	c.IPTablesMode = iptablesMode
	if iptablesMode != "" {
		log.Infof("iptables mode: %s", c.IPTablesMode)
	}
	return nil
}

func (c *Component) Start(_ context.Context) error {
	return nil
}

func (c *Component) Stop() error {
	return nil
}

// The commands that k0s links to the selected xtables multi-call executable.
var xtablesCommands = []string{"iptables", "iptables-save", "iptables-restore", "ip6tables", "ip6tables-save", "ip6tables-restore"}

func xtablesExecutable(mode string) string {
	return fmt.Sprintf("xtables-%s-multi", mode)
}

// extractIPTablesBinaries stages the xtables executables and makes the symlinks
// to the backend detected by DetectHostIPTablesMode.
//
// The xtables executables are taken from the k0s executable, if embedded.
// Otherwise, they are looked up in k0sBinDir and in the PATH, so that k0s builds
// that don't embed them use the host's iptables. If no xtables executable can be
// found at all, no symlinks are made and the host's iptables, if any, is used
// as is. In that case, the returned mode is empty.
//
// extractIPTablesBinaries only works on linux, if called in another OS it will return an error.
func extractIPTablesBinaries(k0sBinDir string, iptablesMode string) (string, error) {
	xtables := make(map[string]string, 2)
	for _, mode := range []string{ModeLegacy, ModeNFT} {
		path, err := assets.StageExecutable(k0sBinDir, xtablesExecutable(mode))
		if err != nil {
			if !errors.Is(err, exec.ErrNotFound) {
				return "", err
			}
			logrus.WithError(err).Debugf("iptables-%s is not available", mode)
			continue
		}
		xtables[mode] = path
	}

	switch {
	case iptablesMode != "" && iptablesMode != "auto":
		if _, ok := xtables[iptablesMode]; !ok {
			return "", fmt.Errorf("iptables mode %q requested, but %s is neither embedded in k0s nor found in %s or in the PATH", iptablesMode, xtablesExecutable(iptablesMode), k0sBinDir)
		}

	case len(xtables) == 0:
		logrus.Warn("No xtables executable is embedded in k0s or installed on the host, using the host's iptables from the PATH, if any")
		return "", removeXtablesSymlinks(k0sBinDir)

	case len(xtables) == 1:
		for mode := range xtables {
			iptablesMode = mode
		}
		logrus.Infof("Only iptables-%s is available", iptablesMode)

	default:
		var err error
		iptablesMode, err = DetectHostIPTablesMode(k0sBinDir)
		if err != nil {
			if kernelMajorVersion() < 5 {
				iptablesMode = ModeLegacy
			} else {
				iptablesMode = ModeNFT
			}
			logrus.WithError(err).Infof("Failed to detect iptables mode, using iptables-%s by default", iptablesMode)
		}
	}

	logrus.Infof("using iptables-%s", iptablesMode)

	// Link relative to the bin directory if the executable has been staged
	// there, otherwise use the absolute path of the host's executable.
	oldpath := xtables[iptablesMode]
	if filepath.Dir(oldpath) == filepath.Clean(k0sBinDir) {
		oldpath = filepath.Base(oldpath)
	}
	for _, symlink := range xtablesCommands {
		symlinkPath := filepath.Join(k0sBinDir, symlink)

		// remove if it exist and ignore error if it doesn't
		_ = os.Remove(symlinkPath)

		err := os.Symlink(oldpath, symlinkPath)
		if err != nil {
			return "", fmt.Errorf("failed to create symlink %s: %w", symlink, err)
		}
	}

	return iptablesMode, nil
}

// removeXtablesSymlinks removes the symlinks to an xtables executable that a
// previous run made in k0sBinDir, so that they don't shadow the host's iptables.
// Anything that's not such a symlink is left untouched.
func removeXtablesSymlinks(k0sBinDir string) error {
	var errs []error
	for _, symlink := range xtablesCommands {
		symlinkPath := filepath.Join(k0sBinDir, symlink)
		target, err := os.Readlink(symlinkPath)
		if err != nil {
			continue // not a symlink, or doesn't exist
		}
		switch filepath.Base(target) {
		case xtablesExecutable(ModeLegacy), xtablesExecutable(ModeNFT):
			if err := os.Remove(symlinkPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// LookPath returns the path of the named iptables executable. It prefers the
// one in k0sBinDir, which is where k0s stages its own, and falls back to a PATH
// lookup for builds of k0s that don't embed iptables.
func LookPath(k0sBinDir, name string) (string, error) {
	path := filepath.Join(k0sBinDir, name+constant.ExecutableSuffix)
	if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
		return path, nil
	}
	return exec.LookPath(name)
}

func kernelMajorVersion() byte {
	if runtime.GOOS != "linux" {
		return 0
	}
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return 0
	}
	return data[0] - '0'
}

// DetectHostIPTablesMode figure out whether iptables-legacy or iptables-nft is in use on the host.
// Follows the same logic as kube-proxy/kube-route.
// See: https://github.com/kubernetes-sigs/iptables-wrappers/blob/master/iptables-wrapper-installer.sh
func DetectHostIPTablesMode(k0sBinPath string) (string, error) {
	logrus.Info("Trying to detect iptables mode")

	nftMatches, nftTotal, nftErr := findMatchingEntries(k0sBinPath, ModeNFT, "KUBE-IPTABLES-HINT", "KUBE-KUBELET-CANARY")
	if nftErr != nil {
		logrus.WithError(nftErr).Debug("Failed to inspect iptables rules in nft mode")
		nftErr = fmt.Errorf("nft: %w", nftErr)
	} else if nftMatches {
		logrus.Infof("Some kube-related iptables entries found for iptables-nft")
		return ModeNFT, nil
	}

	legacyMatches, legacyTotal, legacyErr := findMatchingEntries(k0sBinPath, ModeLegacy, "KUBE-IPTABLES-HINT", "KUBE-KUBELET-CANARY")
	if legacyErr != nil {
		logrus.WithError(legacyErr).Debug("Failed to inspect iptables rules in legacy mode")
		legacyErr = fmt.Errorf("legacy: %w", legacyErr)
	} else if legacyMatches {
		logrus.Infof("Some kube-related iptables entries found for iptables-legacy")
		return ModeLegacy, nil
	}

	if nftErr == nil && legacyErr == nil && legacyTotal > nftTotal {
		logrus.Infof(
			"No kube-related entries found in neither iptables-nft nor iptables-legacy, "+
				"but there are more legacy entries than nft entries (%d vs. %d), "+
				"so go with iptables-legacy",
			legacyTotal, nftTotal,
		)
		return ModeLegacy, nil
	}

	iptablesPath, err := exec.LookPath("iptables")
	if err != nil {
		return "", errors.Join(err, nftErr, legacyErr)
	}

	out, err := exec.Command(iptablesPath, "--version").CombinedOutput()
	if err != nil {
		return "", errors.Join(err, nftErr, legacyErr)
	}

	outStr := strings.TrimSpace(string(out))
	mode := ModeLegacy

	if strings.Contains(outStr, "nf_tables") {
		mode = ModeNFT
	}

	logrus.Infof("Selecting iptables-%s: %s --version: %s", mode, iptablesPath, outStr)
	return mode, nil
}

func findMatchingEntries(k0sBinPath, mode string, entries ...string) (entriesFound bool, total uint, _ error) {
	binaryPath, err := LookPath(k0sBinPath, xtablesExecutable(mode))
	if err != nil {
		return false, 0, err
	}

	findMatches := func(subcommand string) error {
		cmd := exec.Command(binaryPath, subcommand)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}

		scanner := bufio.NewScanner(out)
		scanner.Split(bufio.ScanLines)
		for scanner.Scan() {
			total++
			if !entriesFound {
				line := scanner.Text()
				for _, entry := range entries {
					if strings.Contains(line, entry) {
						entriesFound = true
					}
				}
			}
		}

		return cmd.Wait()
	}

	v4Err, v6Err := findMatches("iptables-save"), findMatches("ip6tables-save")
	if v4Err != nil && v6Err != nil {
		return false, 0, fmt.Errorf("iptables-save: %w; ip6tables-save: %w", v4Err, v6Err)
	}

	return entriesFound, total, nil
}
