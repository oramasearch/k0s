// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/distribution/reference"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/build"
	"github.com/k0sproject/k0s/pkg/constant"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func liquidImageInventory(env TargetEnv) ([]*liquidChart, error) {
	if env.Spec.Liquid == nil || env.Platform.OS != "linux" {
		return nil, nil
	}
	if env.Spec.Liquid.Components == nil {
		return nil, fmt.Errorf("liquid component declarations are required")
	}
	var charts []*liquidChart
	for _, entry := range env.Spec.Liquid.Components.Entries() {
		if !entry.Component.Enabled || entry.Component.Replacement == v1beta1.LiquidReplacementExternal {
			continue
		}
		if entry.Component.Replacement != v1beta1.LiquidReplacementManaged {
			return nil, fmt.Errorf("liquid component %s: unknown replacement %q", entry.Name, entry.Component.Replacement)
		}
		if env.Platform.Architecture != "amd64" && env.Platform.Architecture != "arm64" {
			return nil, fmt.Errorf("managed liquid components require linux/amd64 or linux/arm64; got linux/%s", env.Platform.Architecture)
		}
		component, err := loadLiquidChart(env, entry)
		if err != nil {
			return nil, fmt.Errorf("liquid component %s: %w (supply exact archives with --chart-dir for offline use)", entry.Name, err)
		}
		component.images, err = renderLiquidImages(env.Spec.Liquid, component)
		if err != nil {
			return nil, fmt.Errorf("liquid component %s: %w", entry.Name, err)
		}
		charts = append(charts, component)
	}
	return charts, nil
}

func renderLiquidImages(liquid *v1beta1.LiquidSpec, component *liquidChart) ([]string, error) {
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(liquid.LiquidComponentValues(component.name)), &values); err != nil {
		return nil, fmt.Errorf("decode declared values: %w", err)
	}
	if err := action.CheckDependencies(component.chart, component.chart.Metadata.Dependencies); err != nil {
		return nil, fmt.Errorf("chart dependencies must be packaged before transfer: %w", err)
	}
	if err := chartutil.ProcessDependencies(component.chart, values); err != nil {
		return nil, err
	}
	if err := validateLiquidRuntimeImageValues(component, values); err != nil {
		return nil, err
	}
	var images []string
	for _, upgrade := range []bool{false, true} {
		manifests, err := renderLiquidChart(component.chart, component.name, values, upgrade)
		if err != nil {
			return nil, err
		}
		for filename, manifest := range manifests {
			if strings.HasSuffix(filename, "/NOTES.txt") {
				continue
			}
			selected, err := imagesFromManifest(manifest)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", filename, err)
			}
			images = append(images, selected...)
		}
	}
	images = uniqueImages(images)
	slices.Sort(images)
	return images, nil
}

// Longhorn can fall back to image names compiled into its controller when a
// CSI value is empty. Those references cannot be recovered from rendered YAML.
func validateLiquidRuntimeImageValues(component *liquidChart, overrides map[string]any) error {
	if component.name != v1beta1.LiquidComponentDistributedStorage || component.chart.Metadata.Name != "longhorn" {
		return nil
	}
	values, err := chartutil.CoalesceValues(component.chart, overrides)
	if err != nil {
		return err
	}
	for _, image := range []string{"attacher", "provisioner", "nodeDriverRegistrar", "resizer", "snapshotter", "livenessProbe"} {
		for _, part := range []string{"repository", "tag"} {
			path := "image.csi." + image + "." + part
			value, err := values.PathValue(path)
			text, ok := value.(string)
			if err != nil || !ok || text == "" {
				return fmt.Errorf("%s must be explicit; controller fallback images cannot be inventoried", path)
			}
		}
	}
	return nil
}

func renderLiquidChart(component *chart.Chart, name string, values map[string]any, upgrade bool) (map[string]string, error) {
	capabilities := chartutil.DefaultCapabilities.Copy()
	version, err := chartutil.ParseKubeVersion(cmp.Or(build.KubernetesVersion, constant.KubeProxyImageVersion))
	if err != nil {
		return nil, err
	}
	capabilities.KubeVersion = *version
	options := chartutil.ReleaseOptions{
		Name:      v1beta1.LiquidComponentChartResourceName(name),
		Namespace: v1beta1.LiquidComponentTargetNamespace(name),
		Revision:  1,
		IsInstall: !upgrade,
		IsUpgrade: upgrade,
	}
	renderValues, err := chartutil.ToRenderValues(component, values, options, capabilities)
	if err != nil {
		return nil, err
	}
	return engine.Render(component, renderValues)
}

// imagesFromManifest includes Pod images, controller image flags and environment
// variables, and Pod templates stored in ConfigMaps. Helm hooks are ordinary
// rendered manifests here, so install, upgrade and test hooks are included too.
func imagesFromManifest(manifest string) ([]string, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	var images []string
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err == io.EOF {
			return uniqueImages(images), nil
		} else if err != nil {
			return nil, err
		}
		if err := collectManifestImages(object, &images); err != nil {
			return nil, err
		}
		if object["kind"] == "ConfigMap" {
			if err := collectConfigMapImages(object, &images); err != nil {
				return nil, err
			}
		}
	}
}

func collectManifestImages(value any, images *[]string) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "image" || strings.HasSuffix(key, "-image") {
				if image, ok := child.(string); ok {
					if err := addManifestImage(images, image); err != nil {
						return err
					}
				}
			}
			if key == "command" || key == "args" {
				if args, ok := child.([]any); ok {
					if err := collectImageArguments(args, images); err != nil {
						return err
					}
				}
			}
			if err := collectManifestImages(child, images); err != nil {
				return err
			}
		}
		if name, ok := value["name"].(string); ok && strings.HasSuffix(name, "_IMAGE") {
			image, ok := value["value"].(string)
			if !ok {
				return fmt.Errorf("image environment variable %s must contain a literal value", name)
			}
			return addManifestImage(images, image)
		}
	case []any:
		for _, child := range value {
			if err := collectManifestImages(child, images); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectImageArguments(args []any, images *[]string) error {
	for index, arg := range args {
		text, ok := arg.(string)
		if !ok {
			continue
		}
		flag, image, inline := strings.Cut(text, "=")
		if !strings.HasPrefix(flag, "--") || !strings.HasSuffix(flag, "-image") {
			continue
		}
		if !inline && index+1 < len(args) {
			image, _ = args[index+1].(string)
		}
		if err := addManifestImage(images, image); err != nil {
			return fmt.Errorf("%s: %w", flag, err)
		}
	}
	return nil
}

func collectConfigMapImages(object map[string]any, images *[]string) error {
	data, _ := object["data"].(map[string]any)
	for name, value := range data {
		text, ok := value.(string)
		if !ok || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".json")) {
			continue
		}
		var document any
		decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(text), 4096)
		for {
			if err := decoder.Decode(&document); err == io.EOF {
				break
			} else if err != nil {
				return fmt.Errorf("ConfigMap data %s: %w", name, err)
			}
			if err := collectManifestImages(document, images); err != nil {
				return err
			}
		}
	}
	return nil
}

func addManifestImage(images *[]string, image string) error {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return fmt.Errorf("invalid rendered image %q: %w", image, err)
	}
	_, tagged := named.(reference.NamedTagged)
	_, digested := named.(reference.Canonical)
	if !tagged && !digested {
		return fmt.Errorf("rendered image %q must have a tag or digest", image)
	}
	*images = append(*images, image)
	return nil
}

func uniqueImages(images []string) []string {
	seen := make(map[string]bool, len(images))
	var result []string
	for _, image := range images {
		if !seen[image] {
			seen[image] = true
			result = append(result, image)
		}
	}
	return result
}
