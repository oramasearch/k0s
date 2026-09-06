//go:build unix

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeimage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	configfile "github.com/k0sproject/k0s/pkg/nodeconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type recordedCommand struct {
	name string
	args []string
}

type recordingRunner struct {
	commands []recordedCommand
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.commands = append(r.commands, recordedCommand{name: name, args: args})
	return nil
}

func TestImageSwitchRequiresARebootBeforeHealthy(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proc/sys/kernel/random"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/kernel/random/boot_id"), []byte("boot-one\n"), 0o644))
	instruction := ImageInstruction{
		Plan:   "upgrade-1",
		Digest: "registry.example/node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Action: "switch",
	}
	encoded, err := json.Marshal(instruction)
	require.NoError(t, err)
	node := readyNode("node-a")
	node.Annotations = map[string]string{NodeImageInstructionAnnotation: string(encoded), NodeImageCurrentAnnotation: "old"}
	client := fake.NewSimpleClientset(node)
	runner := &recordingRunner{}
	options := AgentOptions{NodeName: node.Name, Root: root, Client: client, Runner: runner}

	require.NoError(t, reconcileImageInstruction(t.Context(), options, node))
	require.Len(t, runner.commands, 2)
	assert.Equal(t, "bootc", runner.commands[0].name)
	updated, err := client.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "rebooting:upgrade-1", updated.Annotations[NodeImageStateAnnotation])

	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/kernel/random/boot_id"), []byte("boot-two\n"), 0o644))
	require.NoError(t, reconcileImageInstruction(t.Context(), options, updated))
	updated, err = client.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "healthy:upgrade-1", updated.Annotations[NodeImageStateAnnotation])
	assert.Equal(t, instruction.Digest, updated.Annotations[NodeImageCurrentAnnotation])
}

func TestBreakGlassOpensAndClosesWithAudit(t *testing.T) {
	var events []auditEvent
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var event auditEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		events = append(events, event)
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	root := t.TempDir()
	policy := configfile.Spec{BreakGlass: configfile.BreakGlassPolicy{Enabled: true, MaxTTLSeconds: 3600, AuditEndpoint: server.URL}}
	require.NoError(t, writeJSON(filepath.Join(root, "etc/k0s/node-config.json"), policy))
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	instruction := BreakGlassInstruction{Name: "incident-1", UID: "uid-1", Principal: "operator@example", Reason: "recover etcd", ExpiresAt: now.Add(time.Minute)}
	encoded, err := json.Marshal(instruction)
	require.NoError(t, err)
	node := readyNode("node-a")
	node.Annotations = map[string]string{BreakGlassInstructionAnnotation: string(encoded)}
	client := fake.NewSimpleClientset(node)
	runner := &recordingRunner{}
	options := AgentOptions{NodeName: node.Name, Root: root, Client: client, Runner: runner, HTTPClient: server.Client(), Now: func() time.Time { return now }}

	require.NoError(t, reconcileBreakGlassInstruction(t.Context(), options, node))
	require.Len(t, events, 1)
	assert.Equal(t, "break_glass_open", events[0].Type)
	assert.Equal(t, "systemctl", runner.commands[0].name)

	now = now.Add(2 * time.Minute)
	updated, err := client.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, reconcileBreakGlassInstruction(t.Context(), options, updated))
	require.Len(t, events, 2)
	assert.Equal(t, "break_glass_close", events[1].Type)
	updated, err = client.CoreV1().Nodes().Get(t.Context(), node.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "closed:uid-1", updated.Annotations[BreakGlassStateAnnotation])
}

func TestRollbackSupersedesPendingSwitch(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proc/sys/kernel/random"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/kernel/random/boot_id"), []byte("boot-one\n"), 0o644))
	switchInstruction := ImageInstruction{Plan: "upgrade-1", Digest: "registry.example/node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Action: "switch"}
	marker := pendingImage{Instruction: switchInstruction, PreviousDigest: "old", BootID: "boot-one"}
	require.NoError(t, writeJSON(filepath.Join(root, pendingImagePath), marker))
	rollbackInstruction := switchInstruction
	rollbackInstruction.Action = "rollback"
	encoded, err := json.Marshal(rollbackInstruction)
	require.NoError(t, err)
	node := readyNode("node-a")
	node.Annotations = map[string]string{
		NodeImageInstructionAnnotation: string(encoded),
		NodeImagePreviousAnnotation:    "old",
	}
	client := fake.NewSimpleClientset(node)
	runner := &recordingRunner{}
	options := AgentOptions{NodeName: node.Name, Root: root, Client: client, Runner: runner}

	require.NoError(t, reconcileImageInstruction(t.Context(), options, node))
	require.Len(t, runner.commands, 2)
	assert.Equal(t, recordedCommand{name: "bootc", args: []string{"rollback"}}, runner.commands[0])
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
}
