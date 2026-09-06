//go:build unix

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeimage

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	configfile "github.com/k0sproject/k0s/pkg/nodeconfig"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	pendingImagePath     = "var/lib/k0s/node-image-pending.json"
	activeBreakGlassPath = "run/k0s/break-glass.json"
	principalsPath       = "run/k0s/break-glass-principals"
)

// CommandRunner executes host lifecycle commands.
type CommandRunner interface {
	Run(context.Context, string, ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// AgentOptions configures the immutable-node host agent.
type AgentOptions struct {
	NodeName   string
	Root       string
	Interval   time.Duration
	Client     kubernetes.Interface
	Runner     CommandRunner
	HTTPClient *http.Client
	Now        func() time.Time
}

// RunAgent reconciles host instructions until the context is canceled.
func RunAgent(ctx context.Context, options AgentOptions) error {
	if options.NodeName == "" {
		return errors.New("node name is required")
	}
	if options.Root == "" {
		options.Root = "/"
	}
	if options.Interval <= 0 {
		options.Interval = 2 * time.Second
	}
	if options.Runner == nil {
		options.Runner = execRunner{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.HTTPClient == nil {
		client, err := auditHTTPClient(options.Root)
		if err != nil {
			return err
		}
		options.HTTPClient = client
	}
	ticker := time.NewTicker(options.Interval)
	defer ticker.Stop()
	for {
		if err := reconcileAgent(ctx, options); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func reconcileAgent(ctx context.Context, options AgentOptions) error {
	if err := reconcileExpiredBreakGlass(ctx, options); err != nil {
		return err
	}
	node, err := options.Client.CoreV1().Nodes().Get(ctx, options.NodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read node instructions: %w", err)
	}
	if err := reconcileImageInstruction(ctx, options, node); err != nil {
		return err
	}
	return reconcileBreakGlassInstruction(ctx, options, node)
}

type pendingImage struct {
	Instruction    ImageInstruction `json:"instruction"`
	PreviousDigest string           `json:"previousDigest,omitempty"`
	BootID         string           `json:"bootID"`
}

func reconcileImageInstruction(ctx context.Context, options AgentOptions, node *corev1.Node) error {
	var instruction ImageInstruction
	rawInstruction := node.Annotations[NodeImageInstructionAnnotation]
	if rawInstruction == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(rawInstruction), &instruction); err != nil {
		return fmt.Errorf("decode node-image instruction: %w", err)
	}
	markerPath := filepath.Join(options.Root, pendingImagePath)
	marker, err := readPendingImage(markerPath)
	if err != nil {
		return err
	}
	bootID, err := os.ReadFile(filepath.Join(options.Root, "proc/sys/kernel/random/boot_id"))
	if err != nil {
		return fmt.Errorf("read boot ID: %w", err)
	}
	currentBootID := strings.TrimSpace(string(bootID))
	if instruction.Action == "switch" && node.Annotations[NodeImageStateAnnotation] == "healthy:"+instruction.Plan && node.Annotations[NodeImageCurrentAnnotation] == instruction.Digest {
		return nil
	}
	if instruction.Action == "rollback" && node.Annotations[NodeImageStateAnnotation] == "rolled-back:"+instruction.Plan {
		return nil
	}
	if marker != nil && instruction.Action == "rollback" && marker.Instruction.Action != "rollback" {
		if err := os.Remove(markerPath); err != nil {
			return err
		}
		marker = nil
	}
	if marker != nil && marker.Instruction.Plan == instruction.Plan && marker.BootID != currentBootID {
		return finishReboot(ctx, options, node, marker)
	}
	if marker != nil {
		return nil
	}
	switch instruction.Action {
	case "switch":
		previous := node.Annotations[NodeImageCurrentAnnotation]
		marker = &pendingImage{Instruction: instruction, PreviousDigest: previous, BootID: currentBootID}
		if err := writeJSON(markerPath, marker); err != nil {
			return err
		}
		if err := options.Runner.Run(ctx, "bootc", "switch", instruction.Digest); err != nil {
			os.Remove(markerPath)
			return err
		}
		annotations := map[string]string{
			NodeImageStateAnnotation:    "rebooting:" + instruction.Plan,
			NodeImagePreviousAnnotation: previous,
		}
		if err := patchNodeAnnotations(ctx, options.Client, options.NodeName, annotations); err != nil {
			return err
		}
		if err := options.Runner.Run(ctx, "systemctl", "reboot"); err != nil {
			os.Remove(markerPath)
			return err
		}
		return nil
	case "rollback":
		marker = &pendingImage{Instruction: instruction, PreviousDigest: node.Annotations[NodeImagePreviousAnnotation], BootID: currentBootID}
		if err := writeJSON(markerPath, marker); err != nil {
			return err
		}
		if err := options.Runner.Run(ctx, "bootc", "rollback"); err != nil {
			os.Remove(markerPath)
			return err
		}
		if err := patchNodeAnnotations(ctx, options.Client, options.NodeName, map[string]string{NodeImageStateAnnotation: "rolling-back:" + instruction.Plan}); err != nil {
			return err
		}
		if err := options.Runner.Run(ctx, "systemctl", "reboot"); err != nil {
			os.Remove(markerPath)
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported node-image action %q", instruction.Action)
	}
}

func finishReboot(ctx context.Context, options AgentOptions, node *corev1.Node, marker *pendingImage) error {
	if !nodeReady(node) {
		return nil
	}
	annotations := map[string]string{}
	if marker.Instruction.Action == "rollback" {
		annotations[NodeImageStateAnnotation] = "rolled-back:" + marker.Instruction.Plan
		annotations[NodeImageCurrentAnnotation] = marker.PreviousDigest
	} else {
		annotations[NodeImageStateAnnotation] = "healthy:" + marker.Instruction.Plan
		annotations[NodeImageCurrentAnnotation] = marker.Instruction.Digest
	}
	if err := patchNodeAnnotations(ctx, options.Client, options.NodeName, annotations); err != nil {
		return err
	}
	return os.Remove(filepath.Join(options.Root, pendingImagePath))
}

type activeBreakGlass struct {
	Instruction BreakGlassInstruction `json:"instruction"`
}

func reconcileBreakGlassInstruction(ctx context.Context, options AgentOptions, node *corev1.Node) error {
	policy, err := readNodePolicy(options.Root)
	if err != nil {
		return err
	}
	activePath := filepath.Join(options.Root, activeBreakGlassPath)
	active, err := readActiveBreakGlass(activePath)
	if err != nil {
		return err
	}
	if active != nil && !options.Now().Before(active.Instruction.ExpiresAt) {
		return closeBreakGlass(ctx, options, policy, active)
	}
	rawInstruction := node.Annotations[BreakGlassInstructionAnnotation]
	if rawInstruction == "" || active != nil {
		return nil
	}
	var instruction BreakGlassInstruction
	if err := json.Unmarshal([]byte(rawInstruction), &instruction); err != nil {
		return fmt.Errorf("decode break-glass instruction: %w", err)
	}
	if err := validateBreakGlassInstruction(instruction, policy, options.Now()); err != nil {
		return err
	}
	event := newAuditEvent("break_glass_open", options.NodeName, instruction, options.Now())
	if err := emitAudit(ctx, options.HTTPClient, policy.AuditEndpoint, event); err != nil {
		return fmt.Errorf("record break-glass open before enabling sshd: %w", err)
	}
	if err := writeFile(filepath.Join(options.Root, principalsPath), []byte(instruction.Principal+"\n"), 0o600); err != nil {
		return err
	}
	active = &activeBreakGlass{Instruction: instruction}
	if err := writeJSON(activePath, active); err != nil {
		return err
	}
	if err := options.Runner.Run(ctx, "systemctl", "start", "sshd.service"); err != nil {
		os.Remove(activePath)
		os.Remove(filepath.Join(options.Root, principalsPath))
		return err
	}
	return patchNodeAnnotations(ctx, options.Client, options.NodeName, map[string]string{BreakGlassStateAnnotation: "open:" + instruction.UID})
}

func reconcileExpiredBreakGlass(ctx context.Context, options AgentOptions) error {
	active, err := readActiveBreakGlass(filepath.Join(options.Root, activeBreakGlassPath))
	if err != nil || active == nil || options.Now().Before(active.Instruction.ExpiresAt) {
		return err
	}
	policy, err := readNodePolicy(options.Root)
	if err != nil {
		return err
	}
	return closeBreakGlass(ctx, options, policy, active)
}

func closeBreakGlass(ctx context.Context, options AgentOptions, policy configfile.BreakGlassPolicy, active *activeBreakGlass) error {
	if err := options.Runner.Run(ctx, "systemctl", "stop", "sshd.service"); err != nil {
		return err
	}
	os.Remove(filepath.Join(options.Root, principalsPath))
	event := newAuditEvent("break_glass_close", options.NodeName, active.Instruction, options.Now())
	if err := emitAudit(ctx, options.HTTPClient, policy.AuditEndpoint, event); err != nil {
		return fmt.Errorf("record break-glass close: %w", err)
	}
	if err := patchNodeAnnotations(ctx, options.Client, options.NodeName, map[string]string{BreakGlassStateAnnotation: "closed:" + active.Instruction.UID}); err != nil {
		return err
	}
	return os.Remove(filepath.Join(options.Root, activeBreakGlassPath))
}

func validateBreakGlassInstruction(instruction BreakGlassInstruction, policy configfile.BreakGlassPolicy, now time.Time) error {
	if !policy.Enabled {
		return errors.New("break-glass is disabled by machine configuration")
	}
	if instruction.UID == "" || instruction.Name == "" || strings.TrimSpace(instruction.Reason) == "" {
		return errors.New("break-glass instruction is incomplete")
	}
	if instruction.Principal == "" || strings.ContainsAny(instruction.Principal, "\r\n") {
		return errors.New("break-glass principal is invalid")
	}
	maximum := now.Add(time.Duration(policy.MaxTTLSeconds) * time.Second)
	if !instruction.ExpiresAt.After(now) || instruction.ExpiresAt.After(maximum) {
		return errors.New("break-glass instruction exceeds the configured lifetime")
	}
	return nil
}

type auditEvent struct {
	Type      string    `json:"type"`
	Node      string    `json:"node"`
	Request   string    `json:"request"`
	Principal string    `json:"principal"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

func newAuditEvent(eventType string, node string, instruction BreakGlassInstruction, now time.Time) auditEvent {
	return auditEvent{
		Type:      eventType,
		Node:      node,
		Request:   instruction.Name,
		Principal: instruction.Principal,
		Reason:    instruction.Reason,
		Timestamp: now.UTC(),
	}
}

func emitAudit(ctx context.Context, client *http.Client, endpoint string, event auditEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("lake audit returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func auditHTTPClient(root string) (*http.Client, error) {
	certificatePath := filepath.Join(root, "etc/k0s/lake/tls.crt")
	keyPath := filepath.Join(root, "etc/k0s/lake/tls.key")
	_, certificateErr := os.Stat(certificatePath)
	_, keyErr := os.Stat(keyPath)
	if os.IsNotExist(certificateErr) && os.IsNotExist(keyErr) {
		return &http.Client{Timeout: 10 * time.Second}, nil
	}
	if certificateErr != nil || keyErr != nil {
		return nil, errors.New("both lake audit TLS certificate and key must be present")
	}
	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load lake audit client certificate: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}, nil
}

// RunBreakGlassSession records login and close events around an interactive shell.
func RunBreakGlassSession(ctx context.Context, root string, nodeName string) error {
	if root == "" {
		root = "/"
	}
	active, err := readActiveBreakGlass(filepath.Join(root, activeBreakGlassPath))
	if err != nil {
		return err
	}
	if active == nil || !time.Now().Before(active.Instruction.ExpiresAt) {
		return errors.New("no active break-glass request")
	}
	policy, err := readNodePolicy(root)
	if err != nil {
		return err
	}
	client, err := auditHTTPClient(root)
	if err != nil {
		return err
	}
	login := newAuditEvent("break_glass_login", nodeName, active.Instruction, time.Now())
	if err := emitAudit(ctx, client, policy.AuditEndpoint, login); err != nil {
		return fmt.Errorf("record break-glass login: %w", err)
	}
	command := exec.CommandContext(ctx, "/bin/bash", "-l")
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	shellErr := command.Run()
	closeEvent := newAuditEvent("break_glass_session_close", nodeName, active.Instruction, time.Now())
	auditErr := emitAudit(context.Background(), client, policy.AuditEndpoint, closeEvent)
	return errors.Join(shellErr, auditErr)
}

func readNodePolicy(root string) (configfile.BreakGlassPolicy, error) {
	data, err := os.ReadFile(filepath.Join(root, "etc/k0s/node-config.json"))
	if err != nil {
		return configfile.BreakGlassPolicy{}, fmt.Errorf("read applied node configuration: %w", err)
	}
	var spec configfile.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return configfile.BreakGlassPolicy{}, fmt.Errorf("decode applied node configuration: %w", err)
	}
	return spec.BreakGlass, nil
}

func readPendingImage(path string) (*pendingImage, error) {
	var marker pendingImage
	found, err := readJSON(path, &marker)
	if !found || err != nil {
		return nil, err
	}
	return &marker, nil
}

func readActiveBreakGlass(path string) (*activeBreakGlass, error) {
	var active activeBreakGlass
	found, err := readJSON(path, &active)
	if !found || err != nil {
		return nil, err
	}
	return &active, nil
}

func readJSON(path string, target any) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false, fmt.Errorf("decode %s: %w", path, err)
	}
	return true, nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'), 0o600)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".k0s-node-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func patchNodeAnnotations(ctx context.Context, client kubernetes.Interface, nodeName string, annotations map[string]string) error {
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": annotations}})
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Nodes().Patch(ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}
