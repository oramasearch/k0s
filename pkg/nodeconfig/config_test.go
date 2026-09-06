// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeconfig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVerifyAndApply(t *testing.T) {
	config, signingKey := signedTestConfig(t)
	data, err := yaml.Marshal(config)
	require.NoError(t, err)

	parsed, err := ParseAndVerify(data, signingKey)
	require.NoError(t, err)

	root := t.TempDir()
	require.NoError(t, parsed.Apply(root, data))

	environment, err := os.ReadFile(filepath.Join(root, "etc/k0s/node-config.env"))
	require.NoError(t, err)
	assert.Equal(t, "K0S_NODE_ROLE=worker\nK0S_JOIN_TOKEN_FILE=/etc/k0s/join-token\nK0S_NODE_AGENT_KUBECONFIG=/var/lib/k0s/kubelet.conf\n", string(environment))
	ca, err := os.ReadFile(filepath.Join(root, "etc/ssh/trusted-user-ca-keys.pem"))
	require.NoError(t, err)
	assert.Equal(t, config.Spec.TrustedUserCA, string(ca))
}

func TestParseRejectsTampering(t *testing.T) {
	config, signingKey := signedTestConfig(t)
	config.Spec.Role = "controller"
	data, err := yaml.Marshal(config)
	require.NoError(t, err)

	_, err = ParseAndVerify(data, signingKey)
	require.ErrorContains(t, err, "signature verification failed")
}

func TestValidateRejectsLongBreakGlassTTL(t *testing.T) {
	config, _ := signedTestConfig(t)
	config.Spec.BreakGlass.MaxTTLSeconds = 3601

	require.ErrorContains(t, config.Validate(), "between 1 and 3600")
}

func signedTestConfig(t *testing.T) (Config, []byte) {
	t.Helper()
	configPublic, configPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	caPublic, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	caSSH, err := ssh.NewPublicKey(caPublic)
	require.NoError(t, err)
	config := Config{
		APIVersion: "liquid.k0sproject.io/v1alpha1",
		Kind:       "NodeConfig",
		Spec: Spec{
			Role:          "worker",
			JoinTokenRef:  "/etc/k0s/join-token",
			TrustedUserCA: string(ssh.MarshalAuthorizedKey(caSSH)),
			BreakGlass: BreakGlassPolicy{
				Enabled:       true,
				MaxTTLSeconds: 3600,
				AuditEndpoint: "https://lake.example/v1/audit/events",
			},
		},
		Signature: Signature{KeyID: "operator-2026"},
	}
	payload, err := config.SignedPayload()
	require.NoError(t, err)
	config.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(configPrivate, payload))
	configSSH, err := ssh.NewPublicKey(configPublic)
	require.NoError(t, err)
	return config, ssh.MarshalAuthorizedKey(configSSH)
}
