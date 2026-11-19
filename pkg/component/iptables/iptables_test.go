// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iptables_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/component/iptables"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectHostIPTablesMode(t *testing.T) {
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	writeScript := func(t *testing.T, parentDir, fileName, content string) {
		path := filepath.Join(parentDir, fileName)
		content = "#!" + sh + " -eu\n\n" + content
		require.NoError(t, file.WriteContentAtomically(path, []byte(content), 0755))

		if runtime.GOOS == "windows" {
			// Add a shim for cmd.exe. Parameter forwarding is imperfect, but
			// sufficient for these tests.
			require.NoError(t, file.WriteContentAtomically(path+".cmd", fmt.Appendf(nil, "@%q %q %%*", sh, path), 0755))
		}
	}

	writeXtables := func(t *testing.T, parentDir, mode, v4Script, v6Script string) {
		content := fmt.Sprintf(
			"case \"$1\" in iptables-save) %s ;; ip6tables-save) %s ;; *) exit 1 ;; esac",
			v4Script, v6Script,
		)

		writeScript(t, parentDir, fmt.Sprintf("xtables-%s-multi", mode), content)
	}

	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)

	t.Run("iptables_not_found", func(t *testing.T) {
		binDir := t.TempDir()

		_, err := iptables.DetectHostIPTablesMode(binDir)

		var execErr *exec.Error
		require.ErrorAs(t, err, &execErr)
		assert.Equal(t, "iptables", execErr.Name)
		assert.ErrorIs(t, execErr.Err, exec.ErrNotFound)
	})

	t.Run("xtables_nft", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "nft",
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 1),
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 1),
		)

		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeNFT, mode)
	})

	t.Run("xtables_legacy", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "legacy",
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 1),
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 1),
		)

		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeLegacy, mode)
	})

	t.Run("xtables_nft_over_legacy", func(t *testing.T) {
		binDir := t.TempDir()

		writeXtables(t, binDir, "nft",
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 1),
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 1),
		)
		writeXtables(t, binDir, "legacy",
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 3),
			strings.Repeat("echo KUBE-IPTABLES-HINT\n", 3),
		)

		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeNFT, mode)
	})

	t.Run("xtables_legacy_over_nft_more_entries", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "nft",
			strings.Repeat("echo FOOBAR\n", 1),
			strings.Repeat("echo FOOBAR\n", 1),
		)
		writeXtables(t, binDir, "legacy",
			strings.Repeat("echo FOOBAR\n", 1),
			strings.Repeat("echo FOOBAR\n", 2),
		)

		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeLegacy, mode)
	})

	t.Run("fallback_to_iptables_if_xtables_nft_over_legacy_more_entries", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "nft",
			strings.Repeat("echo FOOBAR\n", 1),
			strings.Repeat("echo FOOBAR\n", 2),
		)
		writeXtables(t, binDir, "legacy",
			strings.Repeat("echo FOOBAR\n", 1),
			strings.Repeat("echo FOOBAR\n", 1),
		)

		_, err := iptables.DetectHostIPTablesMode(binDir)
		var execErr *exec.Error
		require.ErrorAs(t, err, &execErr)
		assert.Equal(t, "iptables", execErr.Name)
		assert.ErrorIs(t, execErr.Err, exec.ErrNotFound)
	})

	t.Run("xtables_nft_fails", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "nft", "exit 1", "exit 1")
		writeXtables(t, binDir, "legacy", "exit 1", "echo KUBE-IPTABLES-HINT")

		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeLegacy, mode)
	})

	t.Run("xtables_legacy_fails", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "nft", "exit 1", "echo KUBE-IPTABLES-HINT")
		writeXtables(t, binDir, "legacy", "exit 1", "exit 1")

		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeNFT, mode)
	})

	t.Run("xtables_fails", func(t *testing.T) {
		binDir := t.TempDir()
		writeXtables(t, binDir, "nft", "exit 99", "exit 88")
		writeXtables(t, binDir, "legacy", "exit 77", "exit 66")

		_, err := iptables.DetectHostIPTablesMode(binDir)
		var composite interface{ Unwrap() []error }
		require.ErrorAs(t, err, &composite, "No wrapped errors")
		errs := composite.Unwrap()
		require.Len(t, errs, 3)
		assert.ErrorIs(t, errs[0], exec.ErrNotFound)
		assert.ErrorContains(t, errs[1], "99")
		assert.ErrorContains(t, errs[1], "88")
		assert.ErrorContains(t, errs[2], "77")
		assert.ErrorContains(t, errs[2], "66")
	})

	binDir := t.TempDir()
	writeScript(t, pathDir, "iptables", "")
	writeXtables(t, binDir, "nft", "", "")
	writeXtables(t, binDir, "legacy", "", "")

	t.Run("iptables_legacy", func(t *testing.T) {
		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeLegacy, mode)
	})

	writeScript(t, pathDir, "iptables", "echo foo-nf_tables-bar")

	t.Run("iptables_nft", func(t *testing.T) {
		mode, err := iptables.DetectHostIPTablesMode(binDir)
		require.NoError(t, err)
		assert.Equal(t, iptables.ModeNFT, mode)
	})

	writeScript(t, pathDir, "iptables", "exit 1")

	t.Run("iptables_broken", func(t *testing.T) {
		_, err := iptables.DetectHostIPTablesMode(binDir)
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, 1, exitErr.ExitCode())
	})
}

func TestComponent_HostProvidedXtables(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("iptables binaries are only staged on Linux")
	}

	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	// The test executable has no embedded binaries, just like a k0s build
	// without embedded xtables executables.
	writeXtables := func(t *testing.T, dir, mode string) string {
		path := filepath.Join(dir, "xtables-"+mode+"-multi")
		require.NoError(t, file.WriteContentAtomically(path, []byte("#!"+sh+" -eu\n"), 0755))
		return path
	}

	assertSymlinks := func(t *testing.T, binDir, target string) {
		for _, name := range []string{"iptables", "iptables-save", "iptables-restore", "ip6tables", "ip6tables-save", "ip6tables-restore"} {
			link, err := os.Readlink(filepath.Join(binDir, name))
			if assert.NoError(t, err, name) {
				assert.Equal(t, target, link, name)
			}
		}
	}

	t.Run("host_nft_and_legacy_explicit_mode", func(t *testing.T) {
		pathDir, binDir := t.TempDir(), t.TempDir()
		t.Setenv("PATH", pathDir)
		writeXtables(t, pathDir, "legacy")
		nft := writeXtables(t, pathDir, "nft")

		underTest := iptables.Component{IPTablesMode: iptables.ModeNFT, BinDir: binDir}
		require.NoError(t, underTest.Init(t.Context()))
		assert.Equal(t, iptables.ModeNFT, underTest.IPTablesMode)
		assertSymlinks(t, binDir, nft)
	})

	t.Run("host_legacy_only", func(t *testing.T) {
		pathDir, binDir := t.TempDir(), t.TempDir()
		t.Setenv("PATH", pathDir)
		legacy := writeXtables(t, pathDir, "legacy")

		underTest := iptables.Component{BinDir: binDir}
		require.NoError(t, underTest.Init(t.Context()))
		assert.Equal(t, iptables.ModeLegacy, underTest.IPTablesMode)
		assertSymlinks(t, binDir, legacy)
	})

	t.Run("staged_in_bin_dir", func(t *testing.T) {
		binDir := t.TempDir()
		t.Setenv("PATH", t.TempDir())
		writeXtables(t, binDir, "nft")

		underTest := iptables.Component{IPTablesMode: "auto", BinDir: binDir}
		require.NoError(t, underTest.Init(t.Context()))
		assert.Equal(t, iptables.ModeNFT, underTest.IPTablesMode)
		assertSymlinks(t, binDir, "xtables-nft-multi")
	})

	t.Run("requested_mode_unavailable", func(t *testing.T) {
		pathDir, binDir := t.TempDir(), t.TempDir()
		t.Setenv("PATH", pathDir)
		writeXtables(t, pathDir, "nft")

		underTest := iptables.Component{IPTablesMode: iptables.ModeLegacy, BinDir: binDir}
		err := underTest.Init(t.Context())
		assert.ErrorContains(t, err, `iptables mode "legacy" requested, but xtables-legacy-multi is neither embedded in k0s nor found in`)
	})

	t.Run("no_xtables_at_all", func(t *testing.T) {
		binDir := t.TempDir()
		t.Setenv("PATH", t.TempDir())

		// Symlinks left behind by a previous run are removed, so that they
		// don't shadow the host's iptables. Anything else is left alone.
		require.NoError(t, os.Symlink("xtables-nft-multi", filepath.Join(binDir, "iptables")))
		require.NoError(t, os.Symlink("/usr/sbin/xtables-legacy-multi", filepath.Join(binDir, "ip6tables")))
		require.NoError(t, file.WriteContentAtomically(filepath.Join(binDir, "iptables-save"), nil, 0755))
		require.NoError(t, os.Symlink("some-other-tool", filepath.Join(binDir, "iptables-restore")))

		underTest := iptables.Component{BinDir: binDir}
		require.NoError(t, underTest.Init(t.Context()))
		assert.Empty(t, underTest.IPTablesMode)

		assert.NoFileExists(t, filepath.Join(binDir, "iptables"))
		assert.NoFileExists(t, filepath.Join(binDir, "ip6tables"))
		assert.FileExists(t, filepath.Join(binDir, "iptables-save"))
		link, err := os.Readlink(filepath.Join(binDir, "iptables-restore"))
		require.NoError(t, err)
		assert.Equal(t, "some-other-tool", link)
	})

	t.Run("no_xtables_explicit_mode", func(t *testing.T) {
		binDir := t.TempDir()
		t.Setenv("PATH", t.TempDir())

		underTest := iptables.Component{IPTablesMode: iptables.ModeNFT, BinDir: binDir}
		assert.ErrorContains(t, underTest.Init(t.Context()), "xtables-nft-multi is neither embedded in k0s nor found in")
	})
}

func TestLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("iptables is not available on Windows")
	}

	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	pathDir, binDir := t.TempDir(), t.TempDir()
	t.Setenv("PATH", pathDir)

	write := func(t *testing.T, dir, name string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, file.WriteContentAtomically(path, []byte("#!"+sh+" -eu\n"), 0755))
		return path
	}

	t.Run("not_found", func(t *testing.T) {
		_, err := iptables.LookPath(binDir, "iptables")
		assert.ErrorIs(t, err, exec.ErrNotFound)
	})

	hostPath := write(t, pathDir, "iptables")

	t.Run("falls_back_to_path", func(t *testing.T) {
		path, err := iptables.LookPath(binDir, "iptables")
		require.NoError(t, err)
		assert.Equal(t, hostPath, path)
	})

	t.Run("prefers_bin_dir", func(t *testing.T) {
		binPath := write(t, binDir, "iptables")
		path, err := iptables.LookPath(binDir, "iptables")
		require.NoError(t, err)
		assert.Equal(t, binPath, path)
	})
}

func TestMain(m *testing.M) {
	logrus.SetLevel(logrus.DebugLevel)
	os.Exit(m.Run())
}
