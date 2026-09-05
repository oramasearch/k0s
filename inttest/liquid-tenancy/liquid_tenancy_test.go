// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidtenancy

import (
	"context"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/stretchr/testify/suite"
)

const (
	serverNamespace = "tenant-server"
	clientNamespace = "tenant-client"
	alpineImage     = "docker.io/library/alpine:3.24.1"

	prepareCiliumFilesystemCommand = "mount -t bpf bpffs /sys/fs/bpf 2>/dev/null || true; " +
		"mount --make-shared /sys/fs/bpf; " +
		"mount --make-shared /run; " +
		"mkdir -p /run/cilium/cgroupv2; " +
		"mount -t cgroup2 none /run/cilium/cgroupv2 2>/dev/null || true; " +
		"mount --make-shared /run/cilium/cgroupv2"
)

type LiquidTenancySuite struct {
	common.BootlooseSuite
}

func (s *LiquidTenancySuite) TestTenantNamespaceProfile() {
	controller := s.ControllerNode(0)
	_, err := s.RunCommandController(0, prepareCiliumFilesystemCommand)
	s.Require().NoError(err)
	s.Require().NoError(s.InitController(0, "--enable-worker", "--no-taints"))

	client, err := s.KubeClient(controller)
	s.Require().NoError(err)
	restConfig, err := s.GetKubeConfig(controller)
	s.Require().NoError(err)
	s.Require().NoError(s.WaitForNodeReady(controller, client))
	s.Require().NoError(common.WaitForDaemonSet(s.Context(), client, "cilium", metav1.NamespaceSystem))

	s.applyProfile(serverNamespace)
	s.applyProfile(clientNamespace)
	s.assertProfileObjects(client, serverNamespace)
	s.assertPrivilegedPodDenied(client)
	s.assertCrossNamespaceConnectionDenied(client, restConfig)
}

func (s *LiquidTenancySuite) applyProfile(namespace string) {
	command := s.K0sFullPath + " liquid tenant-profile " + namespace +
		" | " + s.K0sFullPath + " kubectl apply -f -"
	output, err := s.RunCommandController(0, command)
	s.Require().NoError(err, output)
	s.T().Logf("applied %s profile:\n%s", namespace, output)
}

func (s *LiquidTenancySuite) assertProfileObjects(client kubernetes.Interface, namespace string) {
	ns, err := client.CoreV1().Namespaces().Get(s.Context(), namespace, metav1.GetOptions{})
	s.Require().NoError(err)
	s.Equal("restricted", ns.Labels["pod-security.kubernetes.io/enforce"])

	_, err = client.NetworkingV1().NetworkPolicies(namespace).Get(s.Context(), "default-deny", metav1.GetOptions{})
	s.Require().NoError(err)
	_, err = client.CoreV1().ResourceQuotas(namespace).Get(s.Context(), "tenant-defaults", metav1.GetOptions{})
	s.Require().NoError(err)
	_, err = client.CoreV1().LimitRanges(namespace).Get(s.Context(), "tenant-defaults", metav1.GetOptions{})
	s.Require().NoError(err)
}

func (s *LiquidTenancySuite) assertPrivilegedPodDenied(client kubernetes.Interface) {
	privileged := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "privileged",
			Namespace: serverNamespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "privileged",
				Image: alpineImage,
				SecurityContext: &corev1.SecurityContext{
					Privileged: &privileged,
				},
			}},
		},
	}

	_, err := client.CoreV1().Pods(serverNamespace).Create(s.Context(), pod, metav1.CreateOptions{})
	s.Require().Error(err)
	s.True(apierrors.IsForbidden(err), err)
	s.Require().ErrorContains(err, "violates PodSecurity")
	s.T().Logf("privileged pod refused: %v", err)
}

func (s *LiquidTenancySuite) assertCrossNamespaceConnectionDenied(client kubernetes.Interface, restConfig *rest.Config) {
	server := restrictedPod(
		"server",
		serverNamespace,
		[]string{"/bin/busybox", "nc", "-lk", "-p", "8080", "-e", "/bin/true"},
	)
	server, err := client.CoreV1().Pods(serverNamespace).Create(s.Context(), server, metav1.CreateOptions{})
	s.Require().NoError(err)
	s.Require().NoError(common.WaitForPod(s.Context(), client, server.Name, server.Namespace))

	clientPod := restrictedPod("client", clientNamespace, []string{"sleep", "3600"})
	clientPod, err = client.CoreV1().Pods(clientNamespace).Create(s.Context(), clientPod, metav1.CreateOptions{})
	s.Require().NoError(err)
	s.Require().NoError(common.WaitForPod(s.Context(), client, clientPod.Name, clientPod.Namespace))

	_, err = common.PodExecCmdOutput(
		client,
		restConfig,
		server.Name,
		server.Namespace,
		"/bin/busybox nc -z -w 2 127.0.0.1 8080",
	)
	s.Require().NoError(err, "server did not answer inside its own network namespace")

	server, err = client.CoreV1().Pods(server.Namespace).Get(s.Context(), server.Name, metav1.GetOptions{})
	s.Require().NoError(err)
	target := net.JoinHostPort(server.Status.PodIP, "8080")
	command := "/bin/busybox nc -z -w 2 " + target

	var denied error
	err = wait.PollUntilContextTimeout(s.Context(), time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		_, denied = common.PodExecCmdOutput(client, restConfig, clientPod.Name, clientPod.Namespace, command)
		return denied != nil, nil
	})
	s.Require().NoError(err)
	s.Require().Error(denied)
	s.T().Logf("cross-namespace connection denied: %v", denied)
}

func restrictedPod(name string, namespace string, command []string) *corev1.Pod {
	allowPrivilegeEscalation := false
	runAsNonRoot := true
	runAsUser := int64(1000)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &runAsNonRoot,
				RunAsUser:    &runAsUser,
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{{
				Name:    name,
				Image:   alpineImage,
				Command: command,
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivilegeEscalation,
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
				},
			}},
		},
	}
}

func TestLiquidTenancySuite(t *testing.T) {
	s := LiquidTenancySuite{
		BootlooseSuite: common.BootlooseSuite{
			ControllerCount: 1,
		},
	}
	suite.Run(t, &s)
}
