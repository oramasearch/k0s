// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	LiquidComponentCilium             = "cilium"
	LiquidComponentGatewayAPI         = "gatewayAPI"
	LiquidComponentCertManager        = "certManager"
	LiquidComponentLogShipper         = "logShipper"
	LiquidComponentLocalPath          = "localPath"
	LiquidComponentDistributedStorage = "distributedStorage"

	LiquidReplacementManaged  = "managed"
	LiquidReplacementExternal = "external"

	LiquidAdmissionModeOff            = "off"
	LiquidAdmissionModeAttestation    = "attestation"
	LiquidAdmissionModeAttestationLog = "attestation+log"
	LiquidAdmissionFailurePolicyFail  = "Fail"
	LiquidAdmissionLogModeDeny        = "deny"
	LiquidAdmissionLogModeVerify      = "verify"
)

// LiquidSpec contains the liquid-specific cluster configuration.
type LiquidSpec struct {
	Components *LiquidComponents `json:"components,omitempty"`
	NodeStore  *LiquidNodeStore  `json:"nodeStore,omitempty"`
	Platform   *LiquidPlatform   `json:"platform,omitempty"`
	Admission  *LiquidAdmission  `json:"admission,omitempty"`
}

// LiquidNodeStore declares the chunk-aware runtime store on workers.
type LiquidNodeStore struct {
	Enabled bool `json:"enabled"`
}

// LiquidPlatform identifies the cluster services used by the developer platform.
type LiquidPlatform struct {
	ACMEDirectory string `json:"acmeDirectory,omitempty"`
	LogsEndpoint  string `json:"logsEndpoint"`
}

// LiquidAdmission declares the cluster default for image-attestation admission.
type LiquidAdmission struct {
	Enabled bool `json:"enabled"`
	// +kubebuilder:validation:Enum=off;attestation;attestation+log
	Mode     string   `json:"mode"`
	Issuers  []string `json:"issuers,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
	Exempt   []string `json:"exempt,omitempty"`
	// +kubebuilder:validation:Enum=Fail
	FailurePolicy    string              `json:"failurePolicy"`
	AttestationStore string              `json:"attestationStore,omitempty"`
	Log              *LiquidAdmissionLog `json:"log,omitempty"`
}

// LiquidAdmissionLog selects transparency-log handling for an admission policy.
type LiquidAdmissionLog struct {
	// +kubebuilder:validation:Enum=deny;verify
	Mode  string                  `json:"mode"`
	Lake  string                  `json:"lake,omitempty"`
	Trust LiquidAdmissionLogTrust `json:"trust,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MaximumCheckpointAgeSeconds int `json:"maximumCheckpointAgeSeconds,omitempty"`
}

// LiquidAdmissionLogTrust contains the pinned Lake identity and witness keys.
type LiquidAdmissionLogTrust struct {
	Identity         LiquidAdmissionLogIdentity  `json:"identity"`
	WitnessThreshold int                         `json:"witnessThreshold"`
	Witnesses        []LiquidAdmissionLogWitness `json:"witnesses,omitempty"`
}

// LiquidAdmissionLogIdentity identifies the signing key for a Lake log.
type LiquidAdmissionLogIdentity struct {
	TAAPID    string `json:"taapID"`
	PublicKey string `json:"publicKey"`
}

// LiquidAdmissionLogWitness identifies one checkpoint witness key.
type LiquidAdmissionLogWitness struct {
	Endpoint  string `json:"endpoint"`
	PublicKey string `json:"publicKey"`
}

// LiquidComponents contains every component bundled by liquid.
type LiquidComponents struct {
	Cilium             LiquidComponent `json:"cilium"`
	GatewayAPI         LiquidComponent `json:"gatewayAPI"`
	CertManager        LiquidComponent `json:"certManager"`
	LogShipper         LiquidComponent `json:"logShipper"`
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
		Platform: &LiquidPlatform{
			LogsEndpoint: "http://loki.liquid-system.svc.cluster.local:3100",
		},
		Admission: &LiquidAdmission{
			Enabled:       true,
			Mode:          LiquidAdmissionModeOff,
			FailurePolicy: LiquidAdmissionFailurePolicyFail,
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
			CertManager: LiquidComponent{
				Enabled: true,
				Version: "v1.21.1",
				Chart: LiquidComponentChart{
					Repository: "https://charts.jetstack.io",
					Name:       "cert-manager",
				},
				Values:      "crds:\n  enabled: true\n  keep: true\nconfig:\n  gatewayAPI:\n    enabled: true\n",
				Replacement: LiquidReplacementManaged,
			},
			LogShipper: LiquidComponent{
				Enabled: true,
				Version: "0.58.0",
				Chart: LiquidComponentChart{
					Repository: "https://helm.vector.dev",
					Name:       "vector",
				},
				Values:      "role: Agent\ncustomConfig:\n  data_dir: /vector-data-dir\n  api:\n    enabled: true\n    address: 0.0.0.0:8686\n  sources:\n    kubernetes_logs:\n      type: kubernetes_logs\n  sinks:\n    loki:\n      type: loki\n      inputs:\n        - kubernetes_logs\n      endpoint: ${LIQUID_LOGS_ENDPOINT}\n      encoding:\n        codec: text\n      labels:\n        namespace: '{{ `{{ kubernetes.pod_namespace }}` }}'\n        pod: '{{ `{{ kubernetes.pod_name }}` }}'\n        container: '{{ `{{ kubernetes.container_name }}` }}'\n",
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
		{Name: LiquidComponentCertManager, Component: &c.CertManager},
		{Name: LiquidComponentLogShipper, Component: &c.LogShipper},
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
	case LiquidComponentCertManager:
		return "liquid-cert-manager"
	case LiquidComponentLogShipper:
		return "liquid-log-shipper"
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
	if l.Platform == nil {
		errs = append(errs, field.Required(field.NewPath("platform"), "the platform endpoints must be declared"))
	} else {
		errs = append(errs, l.Platform.validate(field.NewPath("platform"))...)
	}
	if l.Admission != nil {
		errs = append(errs, l.Admission.Validate(field.NewPath("admission"))...)
		if l.Admission.Enabled && l.Admission.Mode != LiquidAdmissionModeOff &&
			(l.NodeStore == nil || !l.NodeStore.Enabled) && l.Admission.AttestationStore == "" {
			errs = append(errs, field.Required(field.NewPath("admission", "attestationStore"), "an enforcing policy without a node store requires an attestation store"))
		}
	}

	return errs
}

func (p *LiquidPlatform) validate(path *field.Path) []error {
	var errs []error
	if p.ACMEDirectory != "" {
		if err := validateLiquidEndpoint(p.ACMEDirectory); err != nil {
			errs = append(errs, field.Invalid(path.Child("acmeDirectory"), p.ACMEDirectory, err.Error()))
		}
	}
	if err := validateLiquidEndpoint(p.LogsEndpoint); err != nil {
		errs = append(errs, field.Invalid(path.Child("logsEndpoint"), p.LogsEndpoint, err.Error()))
	}
	return errs
}

// Validate checks the admission default is safe for fail-closed webhook wiring.
func (a *LiquidAdmission) Validate(path *field.Path) []error {
	if a == nil {
		return nil
	}

	var errs []error
	if a.Mode != LiquidAdmissionModeOff &&
		a.Mode != LiquidAdmissionModeAttestation &&
		a.Mode != LiquidAdmissionModeAttestationLog {
		errs = append(errs, field.NotSupported(path.Child("mode"), a.Mode, []string{
			LiquidAdmissionModeOff,
			LiquidAdmissionModeAttestation,
			LiquidAdmissionModeAttestationLog,
		}))
	}
	if a.FailurePolicy != LiquidAdmissionFailurePolicyFail {
		errs = append(errs, field.NotSupported(path.Child("failurePolicy"), a.FailurePolicy, []string{
			LiquidAdmissionFailurePolicyFail,
		}))
	}
	if a.Log != nil {
		errs = append(errs, a.Log.validate(path.Child("log"))...)
	}
	return errs
}

func (l *LiquidAdmissionLog) validate(path *field.Path) []error {
	if l.Mode != LiquidAdmissionLogModeDeny && l.Mode != LiquidAdmissionLogModeVerify {
		return []error{field.NotSupported(path.Child("mode"), l.Mode, []string{
			LiquidAdmissionLogModeDeny,
			LiquidAdmissionLogModeVerify,
		})}
	}
	if l.Mode == LiquidAdmissionLogModeDeny {
		return nil
	}

	var errs []error
	if err := validateLiquidEndpoint(l.Lake); err != nil {
		errs = append(errs, field.Invalid(path.Child("lake"), l.Lake, err.Error()))
	}
	if !strings.HasPrefix(l.Trust.Identity.TAAPID, "taap:1:ed25519:") {
		errs = append(errs, field.Invalid(path.Child("trust", "identity", "taapID"), l.Trust.Identity.TAAPID, "an Ed25519 Lake identity is required"))
	}
	if err := validateLiquidAdmissionPublicKey(l.Trust.Identity.PublicKey); err != nil {
		errs = append(errs, field.Invalid(path.Child("trust", "identity", "publicKey"), l.Trust.Identity.PublicKey, err.Error()))
	}
	if l.Trust.WitnessThreshold < 0 {
		errs = append(errs, field.Invalid(path.Child("trust", "witnessThreshold"), l.Trust.WitnessThreshold, "must not be negative"))
	}
	if l.Trust.WitnessThreshold > len(l.Trust.Witnesses) {
		errs = append(errs, field.Invalid(path.Child("trust", "witnessThreshold"), l.Trust.WitnessThreshold, "must not exceed the number of witnesses"))
	}
	if l.MaximumCheckpointAgeSeconds < 0 {
		errs = append(errs, field.Invalid(path.Child("maximumCheckpointAgeSeconds"), l.MaximumCheckpointAgeSeconds, "must not be negative"))
	}
	for index, witness := range l.Trust.Witnesses {
		witnessPath := path.Child("trust", "witnesses").Index(index)
		if err := validateLiquidEndpoint(witness.Endpoint); err != nil {
			errs = append(errs, field.Invalid(witnessPath.Child("endpoint"), witness.Endpoint, err.Error()))
		}
		if err := validateLiquidAdmissionPublicKey(witness.PublicKey); err != nil {
			errs = append(errs, field.Invalid(witnessPath.Child("publicKey"), witness.PublicKey, err.Error()))
		}
	}
	return errs
}

func validateLiquidAdmissionPublicKey(value string) error {
	if len(value) != 64 {
		return errors.New("must be a 32-byte lowercase hexadecimal key")
	}
	if value != strings.ToLower(value) {
		return errors.New("must be a 32-byte lowercase hexadecimal key")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return errors.New("must be a 32-byte lowercase hexadecimal key")
	}
	return nil
}

// Validate checks the cluster platform endpoints.
func (p *LiquidPlatform) Validate() []error {
	if p == nil {
		return nil
	}
	return p.validate(field.NewPath("platform"))
}

func validateLiquidEndpoint(endpoint string) error {
	if endpoint == "" {
		return errors.New("endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("endpoint must be an HTTP or HTTPS URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("endpoint must not contain a query or fragment")
	}
	return nil
}

// LiquidComponentValues resolves cluster-specific placeholders in component values.
func (l *LiquidSpec) LiquidComponentValues(componentName string) string {
	if l == nil || l.Components == nil {
		return ""
	}
	for _, entry := range l.Components.Entries() {
		if entry.Name != componentName {
			continue
		}
		values := entry.Component.Values
		if componentName == LiquidComponentLogShipper && l.Platform != nil {
			return strings.ReplaceAll(values, "${LIQUID_LOGS_ENDPOINT}", fmt.Sprintf("%q", l.Platform.LogsEndpoint))
		}
		return values
	}
	return ""
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
