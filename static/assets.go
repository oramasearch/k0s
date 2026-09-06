// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package static

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"text/template"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	//go:embed _crds
	crds embed.FS
	CRDs fs.FS = subFS(crds, "_crds")
)

var (
	//go:embed manifests/calico
	calicoManifests embed.FS
	CalicoManifests fs.FS = subFS(calicoManifests, "manifests", "calico")
)

var (
	//go:embed manifests/windows
	windowsManifests embed.FS
	WindowsManifests fs.FS = subFS(windowsManifests, "manifests", "windows")
)

var (
	//go:embed manifests/liquid/tenant-namespace/profile.yaml
	tenantNamespaceProfileSource string

	//go:embed manifests/liquid/tenant-namespace/untrusted-runtime-class.yaml
	untrustedRuntimeClassSource string

	//go:embed manifests/liquid/platform/platform.yaml
	liquidPlatformSource string
)

// RenderTenantNamespaceProfile renders the liquid namespace baseline for name.
func RenderTenantNamespaceProfile(name string) ([]byte, error) {
	if problems := validation.IsDNS1123Label(name); len(problems) != 0 {
		return nil, fmt.Errorf("invalid namespace name %q: %s", name, problems[0])
	}

	profile, err := template.New("tenant-namespace-profile").Parse(tenantNamespaceProfileSource)
	if err != nil {
		return nil, fmt.Errorf("parse tenant namespace profile: %w", err)
	}

	var rendered bytes.Buffer
	if err := profile.Execute(&rendered, struct{ Namespace string }{Namespace: name}); err != nil {
		return nil, fmt.Errorf("render tenant namespace profile: %w", err)
	}
	return rendered.Bytes(), nil
}

// UntrustedRuntimeClass returns the liquid Kata RuntimeClass declaration.
func UntrustedRuntimeClass() []byte {
	return []byte(untrustedRuntimeClassSource)
}

// RenderLiquidPlatform renders the cluster-wide liquid platform resources.
func RenderLiquidPlatform(platform *v1beta1.LiquidPlatform) ([]byte, error) {
	if platform == nil {
		return nil, errors.New("liquid platform configuration is required")
	}
	if errs := platform.Validate(); len(errs) != 0 {
		return nil, errors.Join(errs...)
	}

	manifest, err := template.New("liquid-platform").Parse(liquidPlatformSource)
	if err != nil {
		return nil, fmt.Errorf("parse liquid platform manifest: %w", err)
	}

	data := struct {
		ACMEDirectory string
	}{
		ACMEDirectory: strconv.Quote(platform.ACMEDirectory),
	}
	if platform.ACMEDirectory == "" {
		data.ACMEDirectory = ""
	}

	var rendered bytes.Buffer
	if err := manifest.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("render liquid platform manifest: %w", err)
	}
	return rendered.Bytes(), nil
}

func subFS(f fs.FS, segments ...string) fs.FS {
	f, err := fs.Sub(f, path.Join(segments...))
	if err != nil {
		panic(err)
	}
	return f
}
