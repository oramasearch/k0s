// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidlifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"
	helmv1beta1 "github.com/k0sproject/k0s/pkg/apis/helm/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/client/clientset"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/suite"
)

const lifecyclePrepareFilesystems = "mount -t bpf bpffs /sys/fs/bpf 2>/dev/null || true; " +
	"mount --make-shared /sys/fs/bpf; " +
	"mount --make-shared /run; " +
	"mkdir -p /run/cilium/cgroupv2; " +
	"mount -t cgroup2 none /run/cilium/cgroupv2 2>/dev/null || true; " +
	"mount --make-shared /run/cilium/cgroupv2; " +
	"mkdir -p /var/lib/longhorn; " +
	"mount --bind /var/lib/longhorn /var/lib/longhorn; " +
	"mount --make-shared /var/lib/longhorn"

const lifecycleControllerConfig = `
spec:
  api:
    externalAddress: %s
  extensions:
    helm: {}
  network:
    provider: custom
  liquid:
%s
`

type lifecycleSuite struct {
	common.BootlooseSuite
	lifecycleEnvironment []string
	lifecycleArchive     string
	lifecycleRoot        string
}

type chartSnapshot struct {
	version         string
	resourceVersion string
}

func (s *lifecycleSuite) TestLifecycle() {
	s.Require().NotEmpty(os.Getenv("K0S_UPDATE_FROM_PATH"), "the test must start from a previous liquid build")
	s.prepareComponentFilesystems()
	s.initializeCluster()
	s.configureLifecycleAutomation()

	clients := s.lifecycleClients()
	kubeClient := s.kubeClient()
	expectedVersions := declaredVersions()
	beforeUpgrade := s.waitForChartVersions(clients, expectedVersions)

	targetVersion, err := s.RunCommandController(0, "/dist/k0s-new version")
	s.Require().NoError(err)
	s.runLifecycle("upgrade.sh", targetVersion)
	afterUpgrade := s.waitForChartVersions(clients, expectedVersions)
	s.Equal(beforeUpgrade, afterUpgrade, "the binary upgrade changed a declared component Chart")

	s.createWorkload(kubeClient)
	s.runLifecycle("backup.sh", s.lifecycleArchive)
	_, err = s.RunCommandController(0, "test -f /tmp/liquid-lifecycle-storage-backup")
	s.Require().NoError(err)

	s.resetControllers()
	s.runLifecycle("restore.sh", s.lifecycleArchive)
	s.waitForWorkers(kubeClient)
	s.requireWorkloadAvailable(kubeClient)
	s.waitForChartVersions(clients, expectedVersions)
	_, err = s.RunCommandController(0, "test -f /tmp/liquid-lifecycle-storage-restore")
	s.Require().NoError(err)

	s.runLifecycle("replace-node.sh", s.WorkerNode(2))
	s.waitForWorkers(kubeClient)
	s.runLifecycle("replace-node.sh", s.ControllerNode(2))
	s.waitForWorkers(kubeClient)
	s.waitForChartVersions(clients, expectedVersions)

	s.runLifecycle("rotate-certs.sh")
	clients = s.lifecycleClients()
	kubeClient = s.kubeClient()
	s.waitForWorkers(kubeClient)
	s.requireWorkloadAvailable(kubeClient)
	s.waitForChartVersions(clients, expectedVersions)
	s.requireMetricsJobs(kubeClient, []string{"etcd", "kube-controller-manager", "kube-scheduler"})
	s.runLifecycle("check-health.sh")
}

func (s *lifecycleSuite) prepareComponentFilesystems() {
	for index := range s.ControllerCount {
		_, err := s.RunCommandController(index, lifecyclePrepareFilesystems)
		s.Require().NoError(err)
	}
	for index := range s.WorkerCount {
		_, err := s.RunCommandWorker(index, lifecyclePrepareFilesystems)
		s.Require().NoError(err)
	}
}

func (s *lifecycleSuite) initializeCluster() {
	liquidConfig, err := yaml.Marshal(k0sv1beta1.DefaultLiquidSpec())
	s.Require().NoError(err)
	configData := fmt.Sprintf(
		lifecycleControllerConfig,
		s.GetLBAddress(),
		indentYAML(string(liquidConfig), 4),
	)

	var controllerToken string
	for index := range s.ControllerCount {
		node := s.ControllerNode(index)
		s.Require().NoError(s.WaitForSSH(node, 2*time.Minute, time.Second))
		s.PutFile(node, "/tmp/k0s.yaml", configData)
		arguments := []string{
			"--config=/tmp/k0s.yaml",
			"--enable-dynamic-config",
			"--enable-metrics-scraper",
		}
		if index > 0 {
			arguments = append(arguments, controllerToken)
		}
		s.Require().NoError(s.InitController(index, arguments...))
		s.Require().NoError(s.WaitJoinAPI(node))
		if index == 0 {
			token, err := s.GetJoinToken("controller")
			s.Require().NoError(err)
			controllerToken = token
		}
	}

	workerToken, err := s.GetJoinToken("worker")
	s.Require().NoError(err)
	s.Require().NoError(s.RunWorkersWithToken(workerToken))
	s.waitForWorkers(s.kubeClient())
}

func indentYAML(content string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	trimmedContent := strings.TrimSuffix(content, "\n")
	return prefix + strings.ReplaceAll(trimmedContent, "\n", "\n"+prefix)
}

func (s *lifecycleSuite) configureLifecycleAutomation() {
	temporaryDirectory := s.T().TempDir()
	inventoryPath := filepath.Join(temporaryDirectory, "inventory")
	sshConfigPath := filepath.Join(temporaryDirectory, "ssh_config")
	var inventory strings.Builder
	var sshConfig strings.Builder

	for index := range s.ControllerCount {
		s.writeSSHHost(&sshConfig, s.ControllerNode(index))
		fmt.Fprintf(&inventory, "controller %s %s\n", s.ControllerNode(index), s.ControllerNode(index))
	}
	for index := range s.WorkerCount {
		s.writeSSHHost(&sshConfig, s.WorkerNode(index))
		fmt.Fprintf(&inventory, "worker %s %s\n", s.WorkerNode(index), s.WorkerNode(index))
	}
	s.Require().NoError(os.WriteFile(inventoryPath, []byte(inventory.String()), 0600))
	s.Require().NoError(os.WriteFile(sshConfigPath, []byte(sshConfig.String()), 0600))
	s.lifecycleArchive = filepath.Join(temporaryDirectory, "lifecycle-backup.tar.gz")

	root, err := filepath.Abs(filepath.Join("..", ".."))
	s.Require().NoError(err)
	s.lifecycleRoot = root
	s.lifecycleEnvironment = []string{
		"K0S_LIFECYCLE_INVENTORY=" + inventoryPath,
		"K0S_LIFECYCLE_SSH_CONFIG=" + sshConfigPath,
		"K0S_LIFECYCLE_BINARY_URL=http://localhost/dist/k0s-new",
		"K0S_LIFECYCLE_FORCE_UPDATE=true",
		"K0S_LIFECYCLE_REJOIN_CONTROLLERS=true",
		"K0S_LIFECYCLE_CONTROLLER_ARGS=--enable-dynamic-config --enable-metrics-scraper",
		"K0S_LIFECYCLE_STORAGE_BACKUP_COMMAND=touch /tmp/liquid-lifecycle-storage-backup",
		"K0S_LIFECYCLE_STORAGE_RESTORE_COMMAND=touch /tmp/liquid-lifecycle-storage-restore",
		"K0S_LIFECYCLE_CA_ROTATION_BACKUP=" + s.lifecycleArchive,
	}
}

func (s *lifecycleSuite) writeSSHHost(config *strings.Builder, node string) {
	connection, err := s.SSH(s.Context(), node)
	s.Require().NoError(err)
	defer connection.Disconnect()

	fmt.Fprintf(config, "Host %s\n", node)
	fmt.Fprintf(config, "  HostName %s\n", connection.Address)
	fmt.Fprintf(config, "  Port %d\n", connection.Port)
	fmt.Fprintf(config, "  User %s\n", connection.User)
	fmt.Fprintf(config, "  IdentityFile %s\n", connection.KeyPath)
	fmt.Fprintln(config, "  UserKnownHostsFile /dev/null")
}

func (s *lifecycleSuite) runLifecycle(script string, arguments ...string) {
	command := exec.CommandContext(
		s.Context(),
		filepath.Join(s.lifecycleRoot, "hack", "lifecycle", script),
		arguments...,
	)
	command.Dir = s.lifecycleRoot
	command.Env = append(os.Environ(), s.lifecycleEnvironment...)
	output, err := command.CombinedOutput()
	s.T().Logf("%s output:\n%s", script, output)
	s.Require().NoErrorf(err, "%s failed: %s", script, output)
}

func (s *lifecycleSuite) lifecycleClients() clientset.Interface {
	configuration, err := s.GetKubeConfig(s.ControllerNode(0))
	s.Require().NoError(err)
	clients, err := clientset.NewForConfig(configuration)
	s.Require().NoError(err)
	return clients
}

func (s *lifecycleSuite) kubeClient() kubernetes.Interface {
	client, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)
	return client
}

func declaredVersions() map[string]string {
	versions := make(map[string]string)
	for _, entry := range k0sv1beta1.DefaultLiquidSpec().Components.Entries() {
		versions[entry.Name] = entry.Component.Version
	}
	return versions
}

func (s *lifecycleSuite) waitForChartVersions(clients clientset.Interface, expected map[string]string) map[string]chartSnapshot {
	var result map[string]chartSnapshot
	err := wait.PollUntilContextTimeout(s.Context(), time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		charts, err := clients.HelmV1beta1().Charts(metav1.NamespaceSystem).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil
		}
		byName := make(map[string]helmv1beta1.Chart, len(charts.Items))
		for _, chart := range charts.Items {
			byName[chart.Name] = chart
		}
		observed := make(map[string]chartSnapshot, len(expected))
		for componentName, version := range expected {
			chart, found := byName[k0sv1beta1.LiquidComponentChartResourceName(componentName)]
			if !found || chart.Spec.Version != version || chart.Status.Version != version {
				return false, nil
			}
			observed[componentName] = chartSnapshot{
				version:         chart.Status.Version,
				resourceVersion: chart.ResourceVersion,
			}
		}
		result = observed
		return true, nil
	})
	s.Require().NoError(err)
	return result
}

func (s *lifecycleSuite) createWorkload(client kubernetes.Interface) {
	ctx := s.Context()
	replicas := int32(1)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "lifecycle-data",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Mi"),
				},
			},
		},
	}
	_, err := client.CoreV1().PersistentVolumeClaims(metav1.NamespaceDefault).Create(ctx, pvc, metav1.CreateOptions{})
	s.Require().NoError(err)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "lifecycle-workload",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "lifecycle-workload"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "lifecycle-workload"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.31.4-alpine",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/",
									Port: intstr.FromInt(80),
								},
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvc.Name,
							},
						},
					}},
				},
			},
		},
	}
	_, err = client.AppsV1().Deployments(metav1.NamespaceDefault).Create(ctx, deployment, metav1.CreateOptions{})
	s.Require().NoError(err)
	s.requireWorkloadAvailable(client)
}

func (s *lifecycleSuite) requireWorkloadAvailable(client kubernetes.Interface) {
	err := wait.PollUntilContextTimeout(s.Context(), time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployment, err := client.AppsV1().Deployments(metav1.NamespaceDefault).Get(ctx, "lifecycle-workload", metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return deployment.Status.AvailableReplicas == 1, nil
	})
	s.Require().NoError(err)
}

func (s *lifecycleSuite) resetControllers() {
	for index := range s.ControllerCount {
		s.Require().NoError(s.StopController(s.ControllerNode(index)))
		_, err := s.RunCommandController(index, "/usr/local/bin/k0s reset")
		s.Require().NoError(err)
	}
}

func (s *lifecycleSuite) waitForWorkers(client kubernetes.Interface) {
	for index := range s.WorkerCount {
		s.Require().NoError(s.WaitForNodeReady(s.WorkerNode(index), client))
	}
}

func (s *lifecycleSuite) requireMetricsJobs(client kubernetes.Interface, expected []string) {
	err := wait.PollUntilContextTimeout(s.Context(), 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		data, err := client.CoreV1().RESTClient().Get().
			AbsPath("/api/v1/namespaces/k0s-system/services/http:k0s-pushgateway:http/proxy/api/v1/metrics").
			DoRaw(s.Context())
		if err != nil {
			return false, nil
		}
		var metrics struct {
			Data []struct {
				PushTimeSeconds struct {
					Metrics []struct {
						Labels map[string]string `json:"labels"`
						Value  string            `json:"value"`
					} `json:"metrics"`
				} `json:"push_time_seconds"`
			} `json:"data"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(data, &metrics); err != nil || metrics.Status != "success" {
			return false, nil
		}
		jobs := make([]string, 0)
		for _, metricGroup := range metrics.Data {
			for _, metric := range metricGroup.PushTimeSeconds.Metrics {
				if metric.Value > "0" {
					jobs = append(jobs, metric.Labels["job"])
					break
				}
			}
		}
		slices.Sort(jobs)
		slices.Sort(expected)
		return slices.Equal(jobs, expected), nil
	})
	s.Require().NoError(err)
}

func TestLifecycleSuite(t *testing.T) {
	suite.Run(t, &lifecycleSuite{
		BootlooseSuite: common.BootlooseSuite{
			ControllerCount: 3,
			WorkerCount:     3,
			WithLB:          true,
			LaunchMode:      common.LaunchModeOpenRC,
		},
	})
}
