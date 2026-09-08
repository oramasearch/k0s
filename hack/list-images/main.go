// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

// list-images prints every container image (and its version) that k0s may
// pull on the supported liquid worker platforms. Unlike `k0s airgap
// list-images`, it doesn't need a running node or a linux host to build/run,
// since it only touches pkg/airgap and pkg/apis/k0s/v1beta1 directly.
package main

import (
	"fmt"
	"log"
	"slices"

	"github.com/k0sproject/k0s/pkg/airgap"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	imagespecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func main() {
	spec := v1beta1.DefaultClusterSpec()

	// Liquid components support Linux amd64 and arm64 workers.
	seen := make(map[string]struct{})
	var uris []string
	for _, platform := range []imagespecv1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	} {
		images, err := airgap.GetImageURIs(airgap.TargetEnv{Platform: platform, Spec: spec}, true)
		if err != nil {
			log.Fatal(err)
		}
		for _, uri := range images {
			if _, ok := seen[uri]; !ok {
				seen[uri] = struct{}{}
				uris = append(uris, uri)
			}
		}
	}

	slices.Sort(uris)
	for _, uri := range uris {
		fmt.Println(uri)
	}
}
