// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeconfig

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"sigs.k8s.io/yaml"
)

const MaxBreakGlassTTLSeconds = 3600

// Config is the signed first-boot machine configuration.
type Config struct {
	APIVersion string    `json:"apiVersion" yaml:"apiVersion"`
	Kind       string    `json:"kind" yaml:"kind"`
	Spec       Spec      `json:"spec" yaml:"spec"`
	Signature  Signature `json:"signature" yaml:"signature"`
}

// Spec is the machine configuration covered by the signature.
type Spec struct {
	Role          string           `json:"role" yaml:"role"`
	JoinTokenRef  string           `json:"joinTokenRef,omitempty" yaml:"joinTokenRef,omitempty"`
	TrustedUserCA string           `json:"trustedUserCA" yaml:"trustedUserCA"`
	BreakGlass    BreakGlassPolicy `json:"breakGlass" yaml:"breakGlass"`
}

// BreakGlassPolicy declares the local maximum and lake audit endpoint.
type BreakGlassPolicy struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	MaxTTLSeconds int32  `json:"maxTTLSeconds" yaml:"maxTTLSeconds"`
	AuditEndpoint string `json:"auditEndpoint" yaml:"auditEndpoint"`
}

// Signature carries the detached Ed25519 signature and its operator key ID.
type Signature struct {
	KeyID string `json:"keyID" yaml:"keyID"`
	Value string `json:"value" yaml:"value"`
}

type signedConfig struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       Spec   `json:"spec"`
}

// ParseAndVerify parses YAML, validates it, and verifies its detached signature.
func ParseAndVerify(data []byte, trustedSigningKey []byte) (*Config, error) {
	var config Config
	if err := yaml.UnmarshalStrict(data, &config); err != nil {
		return nil, fmt.Errorf("decode node configuration: %w", err)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	publicKey, err := parseEd25519Key(trustedSigningKey)
	if err != nil {
		return nil, fmt.Errorf("parse node-config signing key: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(config.Signature.Value)
	if err != nil {
		return nil, fmt.Errorf("decode node-config signature: %w", err)
	}
	payload, err := config.SignedPayload()
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return nil, errors.New("node-config signature verification failed")
	}
	return &config, nil
}

// SignedPayload returns the stable JSON bytes covered by the signature.
func (c Config) SignedPayload() ([]byte, error) {
	payload, err := json.Marshal(signedConfig{
		APIVersion: c.APIVersion,
		Kind:       c.Kind,
		Spec:       c.Spec,
	})
	if err != nil {
		return nil, fmt.Errorf("encode node-config signature payload: %w", err)
	}
	return payload, nil
}

// Validate rejects configurations that would weaken the immutable-node policy.
func (c Config) Validate() error {
	if c.APIVersion != "liquid.k0sproject.io/v1alpha1" {
		return fmt.Errorf("unsupported node-config apiVersion %q", c.APIVersion)
	}
	if c.Kind != "NodeConfig" {
		return fmt.Errorf("unsupported node-config kind %q", c.Kind)
	}
	switch c.Spec.Role {
	case "controller", "controller+worker":
	case "worker":
		if c.Spec.JoinTokenRef == "" {
			return errors.New("worker node-config requires joinTokenRef")
		}
	default:
		return fmt.Errorf("unsupported k0s role %q", c.Spec.Role)
	}
	if c.Spec.JoinTokenRef != "" && !filepath.IsAbs(c.Spec.JoinTokenRef) {
		return errors.New("joinTokenRef must be an absolute file path")
	}
	ca, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.Spec.TrustedUserCA))
	if err != nil {
		return fmt.Errorf("parse trusted user CA: %w", err)
	}
	if ca.Type() != ssh.KeyAlgoED25519 {
		return fmt.Errorf("trusted user CA must be Ed25519, got %s", ca.Type())
	}
	if !c.Spec.BreakGlass.Enabled {
		return errors.New("break-glass policy must be enabled")
	}
	if c.Spec.BreakGlass.MaxTTLSeconds < 1 || c.Spec.BreakGlass.MaxTTLSeconds > MaxBreakGlassTTLSeconds {
		return errors.New("break-glass maxTTLSeconds must be between 1 and 3600")
	}
	if !strings.HasPrefix(c.Spec.BreakGlass.AuditEndpoint, "https://") {
		return errors.New("break-glass auditEndpoint must use https")
	}
	if c.Signature.KeyID == "" || c.Signature.Value == "" {
		return errors.New("node-config signature keyID and value are required")
	}
	return nil
}

// Apply writes the verified configuration beneath root using atomic replacements.
func (c Config) Apply(root string, source []byte) error {
	files := []struct {
		path string
		mode os.FileMode
		data []byte
	}{
		{"etc/k0s/node-config.yaml", 0o600, source},
		{"etc/k0s/node-config.json", 0o600, mustJSON(c.Spec)},
		{"etc/k0s/node-config.env", 0o600, []byte(c.environmentFile())},
		{"etc/ssh/trusted-user-ca-keys.pem", 0o644, []byte(strings.TrimSpace(c.Spec.TrustedUserCA) + "\n")},
	}
	for _, file := range files {
		if err := atomicWrite(filepath.Join(root, file.path), file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) environmentFile() string {
	agentKubeconfig := "/var/lib/k0s/kubelet.conf"
	if c.Spec.Role == "controller" || c.Spec.Role == "controller+worker" {
		agentKubeconfig = "/var/lib/k0s/pki/admin.conf"
	}
	return fmt.Sprintf(
		"K0S_NODE_ROLE=%s\nK0S_JOIN_TOKEN_FILE=%s\nK0S_NODE_AGENT_KUBECONFIG=%s\n",
		c.Spec.Role,
		c.Spec.JoinTokenRef,
		agentKubeconfig,
	)
}

func parseEd25519Key(data []byte) (ed25519.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, err
	}
	cryptoKey, ok := key.(ssh.CryptoPublicKey)
	if !ok {
		return nil, errors.New("signing key has no crypto public key")
	}
	publicKey, ok := cryptoKey.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("signing key must be Ed25519, got %s", key.Type())
	}
	return publicKey, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s parent: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".node-config-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set %s mode: %w", path, err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func mustJSON(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}
