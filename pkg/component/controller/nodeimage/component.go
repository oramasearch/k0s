// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nodeimage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	liquidv1alpha1 "github.com/k0sproject/k0s/pkg/apis/liquid/v1alpha1"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/leaderelection"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/sirupsen/logrus"
)

const (
	NodeImageInstructionAnnotation  = "liquid.k0sproject.io/node-image-instruction"
	NodeImageStateAnnotation        = "liquid.k0sproject.io/node-image-state"
	NodeImageCurrentAnnotation      = "liquid.k0sproject.io/node-image-current"
	NodeImagePreviousAnnotation     = "liquid.k0sproject.io/node-image-previous"
	BreakGlassInstructionAnnotation = "liquid.k0sproject.io/break-glass-instruction"
	BreakGlassStateAnnotation       = "liquid.k0sproject.io/break-glass-state"
	defaultDeadline                 = 10 * time.Minute
)

// ImageInstruction is the controller-to-host image operation.
type ImageInstruction struct {
	Plan   string `json:"plan"`
	Digest string `json:"digest"`
	Action string `json:"action"`
}

// BreakGlassInstruction is the controller-to-host access operation.
type BreakGlassInstruction struct {
	Name      string    `json:"name"`
	UID       string    `json:"uid"`
	Principal string    `json:"principal"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Component reconciles immutable-node and break-glass resources on the leader.
type Component struct {
	clients       kubeutil.ClientFactoryInterface
	leaderElector leaderelector.Interface
	interval      time.Duration

	mutex sync.Mutex
	stop  func()
}

var _ manager.Component = (*Component)(nil)

// NewComponent creates the liquid node-image controller.
func NewComponent(clients kubeutil.ClientFactoryInterface, elector leaderelector.Interface) *Component {
	return &Component{
		clients:       clients,
		leaderElector: elector,
		interval:      5 * time.Second,
	}
}

// Init implements manager.Component.
func (c *Component) Init(context.Context) error {
	return nil
}

// Start implements manager.Component.
func (c *Component) Start(context.Context) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.stop != nil {
		return nil
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		leaderelection.RunLeaderTasks(ctx, c.leaderElector.CurrentStatus, c.run)
	}()
	c.stop = func() {
		cancel(errors.New("node-image controller is stopping"))
		<-done
	}
	return nil
}

// Stop implements manager.Component.
func (c *Component) Stop() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.stop != nil {
		c.stop()
		c.stop = nil
	}
	return nil
}

func (c *Component) run(ctx context.Context) {
	logger := logrus.WithField("component", "liquid-node-image")
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		if err := c.reconcile(ctx, time.Now()); err != nil && ctx.Err() == nil {
			logger.WithError(err).Error("Failed to reconcile immutable-node resources")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Component) reconcile(ctx context.Context, now time.Time) error {
	kube, err := c.clients.GetClient()
	if err != nil {
		return err
	}
	clientset, err := c.clients.GetK0sClient()
	if err != nil {
		return err
	}
	images, err := clientset.LiquidV1alpha1().NodeImages().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list NodeImages: %w", err)
	}
	for index := range images.Items {
		if err := reconcileImage(ctx, kube, clientset.LiquidV1alpha1().NodeImages(), &images.Items[index], now); err != nil {
			return err
		}
	}
	requests, err := clientset.LiquidV1alpha1().BreakGlasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list BreakGlass resources: %w", err)
	}
	activeNodes := make(map[string]string)
	for index := range requests.Items {
		request := &requests.Items[index]
		if request.Status.Phase != liquidv1alpha1.BreakGlassPhaseClosed && request.Status.Phase != liquidv1alpha1.BreakGlassPhaseFailed {
			if existing := activeNodes[request.Spec.Node]; existing != "" {
				if err := setBreakGlassFailure(ctx, clientset.LiquidV1alpha1().BreakGlasses(), request, "node already has active BreakGlass request "+existing); err != nil {
					return err
				}
				continue
			}
			activeNodes[request.Spec.Node] = request.Name
		}
		if err := reconcileBreakGlass(ctx, kube, clientset.LiquidV1alpha1().BreakGlasses(), request, now); err != nil {
			return err
		}
	}
	return nil
}

type nodeImageClient interface {
	UpdateStatus(context.Context, *liquidv1alpha1.NodeImage, metav1.UpdateOptions) (*liquidv1alpha1.NodeImage, error)
}

type breakGlassClient interface {
	UpdateStatus(context.Context, *liquidv1alpha1.BreakGlass, metav1.UpdateOptions) (*liquidv1alpha1.BreakGlass, error)
}

type nodeClient interface {
	List(context.Context, metav1.ListOptions) (*corev1.NodeList, error)
	Get(context.Context, string, metav1.GetOptions) (*corev1.Node, error)
	Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.Node, error)
}

func reconcileImage(ctx context.Context, kube kubernetes.Interface, client nodeImageClient, image *liquidv1alpha1.NodeImage, now time.Time) error {
	if image.Status.ObservedGeneration == 0 {
		nodes, err := kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list nodes for NodeImage %s: %w", image.Name, err)
		}
		targets, err := BuildTargets(nodes.Items, image.Spec.Selector)
		if err != nil {
			return setImageFailure(ctx, client, image, fmt.Sprintf("invalid selector: %v", err))
		}
		image.Status.ObservedGeneration = image.Generation
		image.Status.ObservedDigest = image.Spec.Digest
		image.Status.Targets = targets
		image.Status.Phase = PlanPhase(targets)
		_, err = client.UpdateStatus(ctx, image, metav1.UpdateOptions{})
		return err
	}
	if image.Generation != image.Status.ObservedGeneration || image.Spec.Digest != image.Status.ObservedDigest {
		return setImageFailure(ctx, client, image, "NodeImage plans are immutable after reconciliation starts")
	}
	active := ActiveTarget(image.Status.Targets)
	if active < 0 {
		phase := PlanPhase(image.Status.Targets)
		if image.Status.Phase == phase {
			return nil
		}
		image.Status.Phase = phase
		_, err := client.UpdateStatus(ctx, image, metav1.UpdateOptions{})
		return err
	}
	target := &image.Status.Targets[active]
	node, err := kube.CoreV1().Nodes().Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		target.State = liquidv1alpha1.NodeImageTargetFailed
		target.Message = "target node disappeared"
		image.Status.Phase = liquidv1alpha1.NodeImagePhaseFailed
		_, updateErr := client.UpdateStatus(ctx, image, metav1.UpdateOptions{})
		return updateErr
	}
	if err != nil {
		return err
	}
	if err := ensureNodeAgentRBAC(ctx, kube, node.Name); err != nil {
		return err
	}
	changed, err := advanceTarget(ctx, kube.CoreV1().Nodes(), image, target, node, now)
	if err != nil {
		return err
	}
	if err := ensureNodeAgentRBAC(ctx, kube, node.Name); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	image.Status.Phase = PlanPhase(image.Status.Targets)
	_, err = client.UpdateStatus(ctx, image, metav1.UpdateOptions{})
	return err
}

func advanceTarget(ctx context.Context, nodes nodeClient, image *liquidv1alpha1.NodeImage, target *liquidv1alpha1.NodeImageTargetStatus, node *corev1.Node, now time.Time) (bool, error) {
	state := node.Annotations[NodeImageStateAnnotation]
	switch target.State {
	case liquidv1alpha1.NodeImageTargetPending:
		var existing ImageInstruction
		rawExisting := node.Annotations[NodeImageInstructionAnnotation]
		if rawExisting != "" && json.Unmarshal([]byte(rawExisting), &existing) == nil && existing.Plan != image.Name {
			state := node.Annotations[NodeImageStateAnnotation]
			if state != "healthy:"+existing.Plan && state != "rolled-back:"+existing.Plan {
				target.State = liquidv1alpha1.NodeImageTargetFailed
				target.Message = "node is active in NodeImage plan " + existing.Plan
				return true, nil
			}
		}
		instruction := ImageInstruction{Plan: image.Name, Digest: image.Status.ObservedDigest, Action: "switch"}
		if err := patchAnnotation(ctx, nodes, node.Name, NodeImageInstructionAnnotation, instruction); err != nil {
			return false, err
		}
		started := metav1.NewTime(now)
		target.StartedAt = &started
		target.State = liquidv1alpha1.NodeImageTargetSwitching
		return true, nil
	case liquidv1alpha1.NodeImageTargetSwitching:
		if state == "rebooting:"+image.Name {
			target.State = liquidv1alpha1.NodeImageTargetRebooting
			target.PreviousDigest = node.Annotations[NodeImagePreviousAnnotation]
			return true, nil
		}
		if deadlineExceeded(image, target, now) {
			return requestRollback(ctx, nodes, image, target, node.Name, now)
		}
	case liquidv1alpha1.NodeImageTargetRebooting:
		if state == "healthy:"+image.Name && nodeReady(node) && node.Annotations[NodeImageCurrentAnnotation] == image.Status.ObservedDigest {
			finished := metav1.NewTime(now)
			target.FinishedAt = &finished
			target.State = liquidv1alpha1.NodeImageTargetHealthy
			return true, nil
		}
		if deadlineExceeded(image, target, now) {
			return requestRollback(ctx, nodes, image, target, node.Name, now)
		}
	case liquidv1alpha1.NodeImageTargetRollingBack:
		if state == "rolled-back:"+image.Name && nodeReady(node) {
			finished := metav1.NewTime(now)
			target.FinishedAt = &finished
			target.State = liquidv1alpha1.NodeImageTargetRolledBack
			return true, nil
		}
		if deadlineExceeded(image, target, now) {
			target.State = liquidv1alpha1.NodeImageTargetFailed
			target.Message = "rollback readiness deadline exceeded"
			return true, nil
		}
	}
	return false, nil
}

func requestRollback(ctx context.Context, nodes nodeClient, image *liquidv1alpha1.NodeImage, target *liquidv1alpha1.NodeImageTargetStatus, nodeName string, now time.Time) (bool, error) {
	if !automaticRollback(image) {
		target.State = liquidv1alpha1.NodeImageTargetFailed
		target.Message = "readiness deadline exceeded"
		return true, nil
	}
	instruction := ImageInstruction{Plan: image.Name, Digest: image.Status.ObservedDigest, Action: "rollback"}
	if err := patchAnnotation(ctx, nodes, nodeName, NodeImageInstructionAnnotation, instruction); err != nil {
		return false, err
	}
	started := metav1.NewTime(now)
	target.StartedAt = &started
	target.State = liquidv1alpha1.NodeImageTargetRollingBack
	target.Message = "readiness deadline exceeded; rollback requested"
	return true, nil
}

func reconcileBreakGlass(ctx context.Context, kube kubernetes.Interface, client breakGlassClient, request *liquidv1alpha1.BreakGlass, now time.Time) error {
	if request.Status.Phase == liquidv1alpha1.BreakGlassPhaseClosed || request.Status.Phase == liquidv1alpha1.BreakGlassPhaseFailed {
		return nil
	}
	if request.Status.ObservedGeneration != 0 && request.Generation != request.Status.ObservedGeneration {
		return setBreakGlassFailure(ctx, client, request, "BreakGlass requests are immutable after reconciliation starts")
	}
	if request.Spec.Node == "" || request.Spec.Principal == "" || strings.TrimSpace(request.Spec.Reason) == "" || request.Spec.TTLSeconds < 1 || request.Spec.TTLSeconds > 3600 {
		return setBreakGlassFailure(ctx, client, request, "node, principal, reason, and ttlSeconds between 1 and 3600 are required")
	}
	node, err := kube.CoreV1().Nodes().Get(ctx, request.Spec.Node, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return setBreakGlassFailure(ctx, client, request, "target node does not exist")
	}
	if err != nil {
		return err
	}
	state := node.Annotations[BreakGlassStateAnnotation]
	uid := string(request.UID)
	if state == "closed:"+uid {
		closed := metav1.NewTime(now)
		request.Status.Phase = liquidv1alpha1.BreakGlassPhaseClosed
		request.Status.ClosedAt = &closed
		_, err := client.UpdateStatus(ctx, request, metav1.UpdateOptions{})
		return err
	}
	if state == "open:"+uid {
		if request.Status.Phase == liquidv1alpha1.BreakGlassPhaseOpen {
			return nil
		}
		opened := metav1.NewTime(now)
		expiresAt := now.Add(time.Duration(request.Spec.TTLSeconds) * time.Second)
		var instruction BreakGlassInstruction
		if err := json.Unmarshal([]byte(node.Annotations[BreakGlassInstructionAnnotation]), &instruction); err == nil {
			expiresAt = instruction.ExpiresAt
		}
		expires := metav1.NewTime(expiresAt)
		request.Status.Phase = liquidv1alpha1.BreakGlassPhaseOpen
		request.Status.OpenedAt = &opened
		request.Status.ExpiresAt = &expires
		_, err := client.UpdateStatus(ctx, request, metav1.UpdateOptions{})
		return err
	}
	if request.Status.Phase == liquidv1alpha1.BreakGlassPhaseOpen {
		return nil
	}
	if request.Status.Phase == liquidv1alpha1.BreakGlassPhasePending {
		var instruction BreakGlassInstruction
		if err := json.Unmarshal([]byte(node.Annotations[BreakGlassInstructionAnnotation]), &instruction); err == nil && instruction.UID == uid {
			return nil
		}
	}
	expiresAt := now.Add(time.Duration(request.Spec.TTLSeconds) * time.Second)
	instruction := BreakGlassInstruction{
		Name:      request.Name,
		UID:       uid,
		Principal: request.Spec.Principal,
		Reason:    request.Spec.Reason,
		ExpiresAt: expiresAt,
	}
	if err := patchAnnotation(ctx, kube.CoreV1().Nodes(), node.Name, BreakGlassInstructionAnnotation, instruction); err != nil {
		return err
	}
	request.Status.Phase = liquidv1alpha1.BreakGlassPhasePending
	request.Status.ObservedGeneration = request.Generation
	expires := metav1.NewTime(expiresAt)
	request.Status.ExpiresAt = &expires
	_, err = client.UpdateStatus(ctx, request, metav1.UpdateOptions{})
	return err
}

func patchAnnotation(ctx context.Context, nodes nodeClient, name string, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{key: string(encoded)},
		},
	})
	if err != nil {
		return err
	}
	_, err = nodes.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func setImageFailure(ctx context.Context, client nodeImageClient, image *liquidv1alpha1.NodeImage, message string) error {
	image.Status.Phase = liquidv1alpha1.NodeImagePhaseFailed
	image.Status.Message = message
	if len(image.Status.Targets) != 0 {
		image.Status.Targets[0].Message = message
	}
	_, err := client.UpdateStatus(ctx, image, metav1.UpdateOptions{})
	return err
}

func setBreakGlassFailure(ctx context.Context, client breakGlassClient, request *liquidv1alpha1.BreakGlass, message string) error {
	request.Status.Phase = liquidv1alpha1.BreakGlassPhaseFailed
	request.Status.Message = message
	_, err := client.UpdateStatus(ctx, request, metav1.UpdateOptions{})
	return err
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func deadlineExceeded(image *liquidv1alpha1.NodeImage, target *liquidv1alpha1.NodeImageTargetStatus, now time.Time) bool {
	if target.StartedAt == nil {
		return false
	}
	deadline := image.Spec.Strategy.Deadline.Duration
	if deadline <= 0 {
		deadline = defaultDeadline
	}
	return !now.Before(target.StartedAt.Add(deadline))
}

func automaticRollback(image *liquidv1alpha1.NodeImage) bool {
	return image.Spec.Strategy.AutomaticRollback == nil || *image.Spec.Strategy.AutomaticRollback
}

func ensureNodeAgentRBAC(ctx context.Context, kube kubernetes.Interface, nodeName string) error {
	hash := sha256.Sum256([]byte(nodeName))
	name := fmt.Sprintf("liquid-node-agent-%x", hash[:8])
	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "k0s-liquid-node-image"},
		},
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"nodes"},
			ResourceNames: []string{nodeName},
			Verbs:         []string{"get", "patch", "update"},
		}},
	}
	existingRole, err := kube.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := kube.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create node-agent role for %s: %w", nodeName, err)
		}
	} else if err != nil {
		return err
	} else if !equalPolicyRules(existingRole.Rules, role.Rules) {
		role.ResourceVersion = existingRole.ResourceVersion
		if _, err := kube.RbacV1().ClusterRoles().Update(ctx, role, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update node-agent role for %s: %w", nodeName, err)
		}
	}
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "k0s-liquid-node-image"},
		},
		Subjects: []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "system:node:" + nodeName, APIGroup: rbacv1.GroupName}},
		RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: name},
	}
	existingBinding, err := kube.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := kube.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create node-agent binding for %s: %w", nodeName, err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if existingBinding.RoleRef == binding.RoleRef && equalSubjects(existingBinding.Subjects, binding.Subjects) {
		return nil
	}
	binding.ResourceVersion = existingBinding.ResourceVersion
	if _, err := kube.RbacV1().ClusterRoleBindings().Update(ctx, binding, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update node-agent binding for %s: %w", nodeName, err)
	}
	return nil
}

func equalPolicyRules(left []rbacv1.PolicyRule, right []rbacv1.PolicyRule) bool {
	return reflect.DeepEqual(left, right)
}

func equalSubjects(left []rbacv1.Subject, right []rbacv1.Subject) bool {
	return reflect.DeepEqual(left, right)
}
