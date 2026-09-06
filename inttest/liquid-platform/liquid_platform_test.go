// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidplatform

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/inttest/common"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/stretchr/testify/suite"
)

const (
	applicationNamespace = "liquid-platform-app"
	secretTestNamespace  = "liquid-platform-secret-test"
	platformHost         = "platform.test"
	workloadName         = "platform-workload"
	alpineImage          = "docker.io/library/alpine:3.24.1"

	prepareCiliumFilesystemCommand = "mount -t bpf bpffs /sys/fs/bpf 2>/dev/null || true; " +
		"mount --make-shared /sys/fs/bpf; " +
		"mount --make-shared /run; " +
		"mkdir -p /run/cilium/cgroupv2; " +
		"mount -t cgroup2 none /run/cilium/cgroupv2 2>/dev/null || true; " +
		"mount --make-shared /run/cilium/cgroupv2; " +
		"mkdir -p /var/lib/longhorn; " +
		"mount --bind /var/lib/longhorn /var/lib/longhorn; " +
		"mount --make-shared /var/lib/longhorn"
)

type LiquidPlatformSuite struct {
	common.BootlooseSuite
}

func (s *LiquidPlatformSuite) TestPlatformSurface() {
	controller := s.ControllerNode(0)
	s.startCluster(controller)

	client, err := s.KubeClient(controller)
	s.Require().NoError(err)
	restConfig, err := s.GetKubeConfig(controller)
	s.Require().NoError(err)
	s.Require().NoError(s.WaitForNodeReady(controller, client))
	s.Require().NoError(common.WaitForDaemonSet(s.Context(), client, "cilium", metav1.NamespaceSystem))

	s.applyManifest("platform-support", platformSupportManifest)
	s.waitForRollout("cert-manager", "deployment/pebble")
	s.waitForRollout("liquid-system", "deployment/loki")
	s.configurePebbleTrust()
	s.waitForPlatformComponents(client)
	s.waitForCondition("gatewayclass/liquid", "Accepted", "")
	s.waitForCondition("gateway/liquid", "Programmed", "liquid-system")
	s.waitForCondition("clusterissuer/lake-acme", "Ready", "")

	s.applyTenantProfile(applicationNamespace)
	s.applyManifest("platform-workload", workloadManifest)
	s.Require().NoError(common.WaitForPod(s.Context(), client, workloadName, applicationNamespace))
	s.applyManifest("platform-certificate", certificateManifest)
	s.waitForCondition("certificate/platform-tls", "Ready", "liquid-system")
	s.applyManifest("platform-route", routeManifest)
	s.waitForCondition("gateway/liquid", "Programmed", "liquid-system")
	s.waitForCondition("httproute/platform", "Accepted", applicationNamespace)

	s.assertTLSRoute(client, restConfig)
	s.assertLogRead(workloadName)
	s.assertHandmadeSecretMountRefused(client)
	s.logTranscript()
}

func (s *LiquidPlatformSuite) startCluster(controller string) {
	_, err := s.RunCommandController(0, s.K0sFullPath+" config create > /tmp/k0s.yaml")
	s.Require().NoError(err)
	_, err = s.RunCommandController(0, prepareCiliumFilesystemCommand)
	s.Require().NoError(err)
	s.Require().NoError(s.InitController(
		0,
		"--config=/tmp/k0s.yaml",
		"--enable-dynamic-config",
		"--enable-worker",
		"--no-taints",
		"--disable-components=metrics-server",
	))
	s.T().Logf("started controller-worker %s", controller)
}

func (s *LiquidPlatformSuite) applyManifest(name string, manifest string) {
	remotePath := "/tmp/" + name + ".yaml"
	s.WriteFileContent(s.ControllerNode(0), remotePath, []byte(manifest))
	output, err := s.RunCommandController(0, s.K0sFullPath+" kubectl apply -f "+remotePath)
	s.Require().NoError(err, output)
	s.T().Logf("applied %s:\n%s", name, output)
}

func (s *LiquidPlatformSuite) applyTenantProfile(namespace string) {
	command := s.K0sFullPath + " liquid tenant-profile " + namespace +
		" | " + s.K0sFullPath + " kubectl apply -f -"
	output, err := s.RunCommandController(0, command)
	s.Require().NoError(err, output)
	s.T().Logf("applied %s tenant profile:\n%s", namespace, output)
}

func (s *LiquidPlatformSuite) waitForRollout(namespace string, resource string) {
	command := s.K0sFullPath + " kubectl rollout status " + resource +
		" --namespace=" + namespace + " --timeout=5m"
	output, err := s.RunCommandController(0, command)
	s.Require().NoError(err, output)
	s.T().Logf("%s", output)
}

func (s *LiquidPlatformSuite) waitForCondition(resource string, condition string, namespace string) {
	command := s.K0sFullPath + " kubectl wait " + resource +
		" --for=condition=" + condition + " --timeout=5m"
	if namespace != "" {
		command += " --namespace=" + namespace
	}
	output, err := s.RunCommandController(0, command)
	s.Require().NoError(err, output)
	s.T().Logf("%s", output)
}

func (s *LiquidPlatformSuite) configurePebbleTrust() {
	patch := map[string]any{
		"spec": map[string]any{
			"liquid": map[string]any{
				"platform": map[string]any{
					"acmeDirectory": "https://pebble:14000/dir",
				},
				"components": map[string]any{
					"certManager": map[string]any{
						"values": certManagerPebbleValues,
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(patch)
	s.Require().NoError(err)
	s.WriteFileContent(s.ControllerNode(0), "/tmp/platform-config-patch.json", encoded)
	command := s.K0sFullPath + " kubectl patch clusterconfig k0s --namespace=kube-system" +
		" --type=merge --patch-file=/tmp/platform-config-patch.json"
	output, err := s.RunCommandController(0, command)
	s.Require().NoError(err, output)
	s.T().Logf("configured Pebble trust:\n%s", output)
	s.waitForRollout("cert-manager", "deployment/cert-manager")
}

func (s *LiquidPlatformSuite) waitForPlatformComponents(client kubernetes.Interface) {
	s.Require().NoError(common.WaitForDaemonSet(
		s.Context(),
		client,
		"liquid-log-shipper-vector",
		metav1.NamespaceSystem,
	))
	output, err := s.RunCommandController(0, s.K0sFullPath+" liquid components")
	s.Require().NoError(err, output)
	s.Contains(output, "certManager")
	s.Contains(output, "logShipper")
	s.T().Logf("component status:\n%s", output)
}

func (s *LiquidPlatformSuite) assertTLSRoute(client kubernetes.Interface, restConfig *rest.Config) {
	service, err := gatewayService(s.Context(), client)
	s.Require().NoError(err)
	pod, targetPort, err := gatewayPod(s.Context(), client, service)
	s.Require().NoError(err)

	dialer, err := testutil.NewPodDialer(restConfig)
	s.Require().NoError(err)
	roots := x509.NewCertPool()
	s.Require().True(roots.AppendCertsFromPEM([]byte(pebbleCA)))
	target := net.JoinHostPort(pod.Name+"."+pod.Namespace, strconv.Itoa(int(targetPort)))
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: platformHost,
		},
		DialContext: func(ctx context.Context, network string, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, target)
		},
		DisableKeepAlives: true,
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}
	defer transport.CloseIdleConnections()

	var responseBody string
	err = wait.PollUntilContextTimeout(s.Context(), 2*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+platformHost+"/", nil)
		if err != nil {
			return false, err
		}
		response, err := httpClient.Do(request)
		if err != nil {
			return false, nil
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return false, nil
		}
		responseBody = string(body)
		return response.StatusCode == http.StatusOK && strings.Contains(responseBody, "platform-ready"), nil
	})
	s.Require().NoError(err)
	s.T().Logf("TLS response through Gateway: %s", strings.TrimSpace(responseBody))
}

func gatewayService(ctx context.Context, client kubernetes.Interface) (*corev1.Service, error) {
	services, err := client.CoreV1().Services("liquid-system").List(ctx, metav1.ListOptions{
		LabelSelector: "gateway.networking.k8s.io/gateway-name=liquid",
	})
	if err != nil {
		return nil, err
	}
	if len(services.Items) != 1 {
		return nil, fmt.Errorf("expected one liquid Gateway service, got %d", len(services.Items))
	}
	return &services.Items[0], nil
}

func gatewayPod(ctx context.Context, client kubernetes.Interface, service *corev1.Service) (*corev1.Pod, int32, error) {
	selector := labels.SelectorFromSet(service.Spec.Selector).String()
	pods, err := client.CoreV1().Pods(service.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, 0, err
	}
	if len(pods.Items) == 0 {
		return nil, 0, errors.New("liquid Gateway service has no pods")
	}
	targetPort, err := servicePortTarget(&pods.Items[0], service.Spec.Ports, 443)
	if err != nil {
		return nil, 0, err
	}
	return &pods.Items[0], targetPort, nil
}

func servicePortTarget(pod *corev1.Pod, ports []corev1.ServicePort, servicePort int32) (int32, error) {
	for _, port := range ports {
		if port.Port != servicePort {
			continue
		}
		if port.TargetPort.Type == intstr.Int {
			return port.TargetPort.IntVal, nil
		}
		for _, container := range pod.Spec.Containers {
			for _, containerPort := range container.Ports {
				if containerPort.Name == port.TargetPort.StrVal {
					return containerPort.ContainerPort, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("Gateway service has no target for port %d", servicePort)
}

func (s *LiquidPlatformSuite) assertLogRead(pod string) {
	command := s.K0sFullPath + " liquid logs " + applicationNamespace + "/" + pod +
		" --since=10m --limit=1000"
	var output string
	err := wait.PollUntilContextTimeout(s.Context(), 2*time.Second, 2*time.Minute, true, func(context.Context) (bool, error) {
		var err error
		output, err = s.RunCommandController(0, command)
		return err == nil && strings.Contains(output, "liquid-platform-log-line"), nil
	})
	s.Require().NoError(err, output)
	s.T().Logf("log read through Loki:\n%s", output)
}

func (s *LiquidPlatformSuite) assertHandmadeSecretMountRefused(client kubernetes.Interface) {
	_, err := client.CoreV1().Namespaces().Create(s.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: secretTestNamespace},
	}, metav1.CreateOptions{})
	s.Require().NoError(err)
	_, err = client.CoreV1().Secrets(secretTestNamespace).Create(s.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "handmade",
			Namespace: secretTestNamespace,
		},
		StringData: map[string]string{"value": "not-delivered-by-lake"},
	}, metav1.CreateOptions{})
	s.Require().NoError(err)
	s.applyTenantProfile(secretTestNamespace)

	pod := restrictedPod("handmade-secret", secretTestNamespace)
	pod.Spec.Volumes = []corev1.Volume{{
		Name: "secret",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: "handmade"},
		},
	}}
	pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
		Name:      "secret",
		MountPath: "/secret",
		ReadOnly:  true,
	}}
	_, err = client.CoreV1().Pods(secretTestNamespace).Create(s.Context(), pod, metav1.CreateOptions{})
	s.Require().Error(err)
	s.True(apierrors.IsForbidden(err), err)
	s.Require().ErrorContains(err, "Pod Secret volumes must name a LakeSealedSecret delivery")
	s.T().Logf("handmade Secret mount refused: %v", err)
}

func restrictedPod(name string, namespace string) *corev1.Pod {
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
				Command: []string{"sleep", "3600"},
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

func (s *LiquidPlatformSuite) logTranscript() {
	commands := []string{
		s.K0sFullPath + " kubectl get gatewayclass,gateway,httproute -A",
		s.K0sFullPath + " kubectl get clusterissuer,certificate -A",
		s.K0sFullPath + " kubectl get validatingadmissionpolicy,validatingadmissionpolicybinding",
	}
	for _, command := range commands {
		output, err := s.RunCommandController(0, command)
		s.Require().NoError(err, output)
		s.T().Logf("$ %s\n%s", command, output)
	}
}

func TestLiquidPlatformSuite(t *testing.T) {
	s := LiquidPlatformSuite{
		BootlooseSuite: common.BootlooseSuite{
			ControllerCount: 1,
		},
	}
	suite.Run(t, &s)
}

const certManagerPebbleValues = `crds:
  enabled: true
  keep: true
config:
  gatewayAPI:
    enabled: true
extraEnv:
  - name: SSL_CERT_FILE
    value: /etc/pebble/pebble.minica.pem
volumes:
  - name: pebble-ca
    configMap:
      name: pebble-ca
volumeMounts:
  - name: pebble-ca
    mountPath: /etc/pebble
    readOnly: true
`

const pebbleCA = `-----BEGIN CERTIFICATE-----
MIIDPzCCAiegAwIBAgIIU0Xm9UFdQxUwDQYJKoZIhvcNAQELBQAwIDEeMBwGA1UE
AxMVbWluaWNhIHJvb3QgY2EgNTM0NWU2MCAXDTI1MDkwMzIzNDAwNVoYDzIxMjUw
OTAzMjM0MDA1WjAgMR4wHAYDVQQDExVtaW5pY2Egcm9vdCBjYSA1MzQ1ZTYwggEi
MA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQC5WgZNoVJandj43kkLyU50vzCZ
alozvdRo3OFiKoDtmqKPNWRNO2hC9AUNxTDJco51Yc42u/WV3fPbbhSznTiOOVtn
Ajm6iq4I5nZYltGGZetGDOQWr78y2gWY+SG078MuOO2hyDIiKtVc3xiXYA+8Hluu
9F8KbqSS1h55yxZ9b87eKR+B0zu2ahzBCIHKmKWgc6N13l7aDxxY3D6uq8gtJRU0
toumyLbdzGcupVvjbjDP11nl07RESDWBLG1/g3ktJvqIa4BWgU2HMh4rND6y8OD3
Hy3H8MY6CElL+MOCbFJjWqhtOxeFyZZV9q3kYnk9CAuQJKMEGuN4GU6tzhW1AgMB
AAGjezB5MA4GA1UdDwEB/wQEAwIChDATBgNVHSUEDDAKBggrBgEFBQcDATASBgNV
HRMBAf8ECDAGAQH/AgEAMB0GA1UdDgQWBBSu8RGpErgYUoYnQuwCq+/ggTiEjDAf
BgNVHSMEGDAWgBSu8RGpErgYUoYnQuwCq+/ggTiEjDANBgkqhkiG9w0BAQsFAAOC
AQEAXDVYov1+f6EL7S41LhYQkEX/GyNNzsEvqxE9U0+3Iri5JfkcNOiA9O9L6Z+Y
bqcsXV93s3vi4r4WSWuc//wHyJYrVe5+tK4nlFpbJOvfBUtnoBDyKNxXzZCxFJVh
f9uc8UejRfQMFbDbhWY/x83y9BDufJHHq32OjCIN7gp2UR8rnfYvlz7Zg4qkJBsn
DG4dwd+pRTCFWJOVIG0JoNhK3ZmE7oJ1N4H38XkZ31NPcMksKxpsLLIS9+mosZtg
4olL7tMPJklx5ZaeMFaKRDq4Gdxkbw4+O4vRgNm3Z8AXWKknOdfgdpqLUPPhRcP4
v1lhy71EhBuXXwRQJry0lTdF+w==
-----END CERTIFICATE-----`

var platformSupportManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: pebble-ca
  namespace: cert-manager
data:
  pebble.minica.pem: |
` + "    " + strings.ReplaceAll(pebbleCA, "\n", "\n    ") + `
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pebble
  namespace: cert-manager
spec:
  replicas: 1
  selector:
    matchLabels: {app: pebble}
  template:
    metadata:
      labels: {app: pebble}
    spec:
      containers:
        - name: pebble
          image: ghcr.io/letsencrypt/pebble:v2.10.1
          args: ["-config", "test/config/pebble-config.json"]
          env:
            - {name: PEBBLE_VA_NOSLEEP, value: "1"}
            - {name: PEBBLE_VA_ALWAYS_VALID, value: "1"}
          ports:
            - {name: acme, containerPort: 14000}
          readinessProbe:
            tcpSocket: {port: acme}
---
apiVersion: v1
kind: Service
metadata:
  name: pebble
  namespace: cert-manager
spec:
  selector: {app: pebble}
  ports:
    - {name: acme, port: 14000, targetPort: acme}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: loki-config
  namespace: liquid-system
data:
  local-config.yaml: |
    auth_enabled: false
    server:
      http_listen_port: 3100
    common:
      instance_addr: 127.0.0.1
      path_prefix: /tmp/loki
      storage:
        filesystem:
          chunks_directory: /tmp/loki/chunks
          rules_directory: /tmp/loki/rules
      replication_factor: 1
      ring:
        kvstore:
          store: inmemory
    schema_config:
      configs:
        - from: 2020-10-24
          store: tsdb
          object_store: filesystem
          schema: v13
          index:
            prefix: index_
            period: 24h
    analytics:
      reporting_enabled: false
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: loki
  namespace: liquid-system
spec:
  replicas: 1
  selector:
    matchLabels: {app: loki}
  template:
    metadata:
      labels: {app: loki}
    spec:
      containers:
        - name: loki
          image: docker.io/grafana/loki:3.7.7
          args: ["-config.file=/etc/loki/local-config.yaml"]
          ports:
            - {name: http, containerPort: 3100}
          readinessProbe:
            httpGet: {path: /ready, port: http}
          volumeMounts:
            - {name: config, mountPath: /etc/loki}
      volumes:
        - name: config
          configMap: {name: loki-config}
---
apiVersion: v1
kind: Service
metadata:
  name: loki
  namespace: liquid-system
spec:
  selector: {app: loki}
  ports:
    - {name: http, port: 3100, targetPort: http}
`

const workloadManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: platform-index
  namespace: liquid-platform-app
data:
  index.html: platform-ready
---
apiVersion: v1
kind: Pod
metadata:
  name: platform-workload
  namespace: liquid-platform-app
  labels: {app: platform-workload}
spec:
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: server
      image: docker.io/library/alpine:3.24.1
      command: ["/bin/sh", "-c"]
      args: ["echo liquid-platform-log-line; cp /input/index.html /tmp/index.html; exec httpd -f -p 8080 -h /tmp"]
      ports:
        - {name: http, containerPort: 8080}
      securityContext:
        allowPrivilegeEscalation: false
        capabilities: {drop: [ALL]}
      volumeMounts:
        - {name: index, mountPath: /input, readOnly: true}
  volumes:
    - name: index
      configMap: {name: platform-index}
---
apiVersion: v1
kind: Service
metadata:
  name: platform-workload
  namespace: liquid-platform-app
spec:
  selector: {app: platform-workload}
  ports:
    - {name: http, port: 8080, targetPort: http}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-gateway
  namespace: liquid-platform-app
spec:
  podSelector:
    matchLabels: {app: platform-workload}
  policyTypes: [Ingress]
  ingress:
    - ports:
        - {protocol: TCP, port: 8080}
`

const certificateManifest = `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: platform-tls
  namespace: liquid-system
spec:
  secretName: platform-tls
  dnsNames: [platform.test]
  issuerRef:
    name: lake-acme
    kind: ClusterIssuer
`

const routeManifest = `apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: liquid
  namespace: liquid-system
spec:
  gatewayClassName: liquid
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces: {from: All}
    - name: https-platform-test
      hostname: platform.test
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        certificateRefs:
          - {kind: Secret, name: platform-tls}
      allowedRoutes:
        namespaces: {from: All}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: platform
  namespace: liquid-platform-app
spec:
  parentRefs:
    - name: liquid
      namespace: liquid-system
      sectionName: https-platform-test
  hostnames: [platform.test]
  rules:
    - backendRefs:
        - {name: platform-workload, port: 8080}
`
