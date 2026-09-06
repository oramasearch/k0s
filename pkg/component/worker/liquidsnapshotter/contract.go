// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidsnapshotter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	TreeAnnotation  = "io.liquid.tree"
	StoreAnnotation = "io.liquid.store"
)

type imageContract struct {
	Tree      string
	StoreURL  string
	HasLayers bool
}

type contractResolver interface {
	Resolve(context.Context, string) (imageContract, bool, error)
}

type contentContractResolver struct {
	store content.Store
}

func (r contentContractResolver) Resolve(ctx context.Context, manifestDigest string) (imageContract, bool, error) {
	parsedDigest, err := digest.Parse(manifestDigest)
	if err != nil {
		return imageContract{}, false, fmt.Errorf("invalid image manifest digest: %w", err)
	}

	manifestBytes, err := content.ReadBlob(ctx, r.store, ocispec.Descriptor{Digest: parsedDigest})
	if err != nil {
		return imageContract{}, false, fmt.Errorf("read image manifest: %w", err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return imageContract{}, false, fmt.Errorf("decode image manifest: %w", err)
	}

	contract := imageContract{
		Tree:      manifest.Annotations[TreeAnnotation],
		StoreURL:  manifest.Config.Annotations[StoreAnnotation],
		HasLayers: len(manifest.Layers) > 0,
	}
	if contract.Tree == "" {
		return imageContract{}, false, nil
	}
	if contract.StoreURL == "" {
		contract.StoreURL, err = r.storeURLFromConfig(ctx, manifest.Config)
		if err != nil {
			return contract, true, err
		}
	}
	if contract.StoreURL == "" {
		return contract, true, fmt.Errorf("liquid image config has no %s value", StoreAnnotation)
	}
	return contract, true, nil
}

func (r contentContractResolver) storeURLFromConfig(ctx context.Context, descriptor ocispec.Descriptor) (string, error) {
	configBytes, err := content.ReadBlob(ctx, r.store, descriptor)
	if err != nil {
		return "", fmt.Errorf("read image config: %w", err)
	}

	var image ocispec.Image
	if err := json.Unmarshal(configBytes, &image); err != nil {
		return "", fmt.Errorf("decode image config: %w", err)
	}
	return image.Config.Labels[StoreAnnotation], nil
}
