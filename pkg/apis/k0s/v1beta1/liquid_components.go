// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	"errors"
	"fmt"
	"net/url"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	LiquidComponentCilium             = "cilium"
	LiquidComponentGatewayAPI         = "gatewayAPI"
	LiquidComponentLocalPath          = "localPath"
	LiquidComponentDistributedStorage = "distributedStorage"

	LiquidReplacementManaged  = "managed"
	LiquidReplacementExternal = "external"
)

// LiquidSpec contains the liquid-specific cluster configuration.
type LiquidSpec struct {
	Components *LiquidComponents `json:"components,omitempty"`
	NodeStore  *LiquidNodeStore  `json:"nodeStore,omitempty"`
}

// LiquidNodeStore declares the chunk-aware runtime store on workers.
type LiquidNodeStore struct {
	Enabled bool `json:"enabled"`
}

// LiquidComponents contains every component bundled by liquid.
type LiquidComponents struct {
	Cilium             LiquidComponent `json:"cilium"`
	GatewayAPI         LiquidComponent `json:"gatewayAPI"`
	LocalPath          LiquidComponent `json:"localPath"`
	DistributedStorage LiquidComponent `json:"distributedStorage"`
}

// LiquidComponent declares a replaceable, versioned component.
type LiquidComponent struct {
	Enabled     bool                 `json:"enabled"`
	Version     string               `json:"version"`
	Chart       LiquidComponentChart `json:"chart"`
	Values      string               `json:"values,omitempty"`
	Replacement string               `json:"replacement"`
}

// LiquidComponentChart identifies a Helm chart and its repository.
type LiquidComponentChart struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
}

// LiquidComponentEntry gives a component its stable configuration key.
type LiquidComponentEntry struct {
	Name      string
	Component *LiquidComponent
}

// DefaultLiquidSpec returns the component versions selected for new liquid clusters.
func DefaultLiquidSpec() *LiquidSpec {
	return &LiquidSpec{
		NodeStore: &LiquidNodeStore{
			Enabled: true,
		},
		Components: &LiquidComponents{
			Cilium: LiquidComponent{
				Enabled: true,
				Version: "1.20.1",
				Chart: LiquidComponentChart{
					Repository: "https://helm.cilium.io",
					Name:       "cilium",
				},
				Values:      "gatewayAPI:\n  enabled: true\noperator:\n  replicas: 1\n",
				Replacement: LiquidReplacementManaged,
			},
			GatewayAPI: LiquidComponent{
				Enabled: true,
				Version: "1.6.0",
				Chart: LiquidComponentChart{
					Repository: "https://wiremind.github.io/wiremind-helm-charts",
					Name:       "gateway-api-crds",
				},
				Replacement: LiquidReplacementManaged,
			},
			LocalPath: LiquidComponent{
				Enabled: true,
				Version: "0.0.38",
				Chart: LiquidComponentChart{
					Repository: "https://charts.containeroo.ch",
					Name:       "local-path-provisioner",
				},
				Values:      "storageClass:\n  defaultClass: true\n",
				Replacement: LiquidReplacementManaged,
			},
			DistributedStorage: LiquidComponent{
				Enabled: true,
				Version: "1.12.1",
				Chart: LiquidComponentChart{
					Repository: "https://charts.longhorn.io",
					Name:       "longhorn",
				},
				Values:      "persistence:\n  defaultClass: false\n",
				Replacement: LiquidReplacementManaged,
			},
		},
	}
}

// Entries returns the component declarations in their stable display order.
func (c *LiquidComponents) Entries() []LiquidComponentEntry {
	if c == nil {
		return nil
	}

	return []LiquidComponentEntry{
		{Name: LiquidComponentCilium, Component: &c.Cilium},
		{Name: LiquidComponentGatewayAPI, Component: &c.GatewayAPI},
		{Name: LiquidComponentLocalPath, Component: &c.LocalPath},
		{Name: LiquidComponentDistributedStorage, Component: &c.DistributedStorage},
	}
}

// LiquidComponentChartResourceName returns the stable Chart object name.
func LiquidComponentChartResourceName(componentName string) string {
	switch componentName {
	case LiquidComponentCilium:
		return "liquid-cilium"
	case LiquidComponentGatewayAPI:
		return "liquid-gateway-api"
	case LiquidComponentLocalPath:
		return "liquid-local-path"
	case LiquidComponentDistributedStorage:
		return "liquid-distributed-storage"
	default:
		return ""
	}
}

// Validate checks the liquid component declarations.
func (l *LiquidSpec) Validate() []error {
	if l == nil {
		return nil
	}
	if l.Components == nil {
		return []error{field.Required(field.NewPath("components"), "all liquid components must be declared")}
	}

	var errs []error
	for _, entry := range l.Components.Entries() {
		componentPath := field.NewPath("components").Child(entry.Name)
		errs = append(errs, entry.Component.validate(componentPath)...)
	}

	return errs
}

func (c *LiquidComponent) validate(path *field.Path) []error {
	var errs []error
	if c.Version == "" {
		errs = append(errs, field.Required(path.Child("version"), "a declared version prevents implicit upgrades"))
	}
	if c.Chart.Name == "" {
		errs = append(errs, field.Required(path.Child("chart", "name"), ""))
	}
	if err := validateLiquidChartRepository(c.Chart.Repository); err != nil {
		errs = append(errs, field.Invalid(path.Child("chart", "repository"), c.Chart.Repository, err.Error()))
	}
	if c.Replacement != LiquidReplacementManaged && c.Replacement != LiquidReplacementExternal {
		errs = append(errs, field.NotSupported(path.Child("replacement"), c.Replacement, []string{LiquidReplacementManaged, LiquidReplacementExternal}))
	}

	return errs
}

func validateLiquidChartRepository(repository string) error {
	if repository == "" {
		return errors.New("repository is required")
	}

	parsed, err := url.Parse(repository)
	if err != nil {
		return fmt.Errorf("invalid repository URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("repository must be an HTTPS URL")
	}

	return nil
}

func (l *LiquidSpec) ciliumEnabled() bool {
	return l != nil && l.Components != nil && l.Components.Cilium.Enabled
}
