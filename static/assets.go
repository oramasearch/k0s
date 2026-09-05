// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package static

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"text/template"

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

func subFS(f fs.FS, segments ...string) fs.FS {
	f, err := fs.Sub(f, path.Join(segments...))
	if err != nil {
		panic(err)
	}
	return f
}
