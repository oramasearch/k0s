// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	paths, err := filepath.Glob("/data/file-*.bin")
	if err != nil {
		fail(err)
	}
	sort.Strings(paths)
	var total int64
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			fail(err)
		}
		count, copyErr := io.Copy(io.Discard, file)
		closeErr := file.Close()
		if copyErr != nil {
			fail(copyErr)
		}
		if closeErr != nil {
			fail(closeErr)
		}
		total += count
	}
	fmt.Printf("files=%d bytes=%d\n", len(paths), total)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
