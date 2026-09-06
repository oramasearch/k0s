// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquidnodestore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/opencontainers/go-digest"
	imagespecs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/yaml"
)

const (
	fileCount        = 8
	fileSize         = 1 << 20
	changedFile      = 7
	firstSyncPort    = 19001
	secondSyncPort   = 19002
	liquidExecutable = "/var/lib/k0s/bin/liquid"
)

type nodeStoreSuite struct {
	common.BootlooseSuite
}

type imageFiles struct {
	runner []byte
	data   [][]byte
}

func (s *nodeStoreSuite) TestChunkAwarePulls() {
	s.configureContainerdRegistry()
	s.initializeCluster()

	client, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)
	s.Require().NoError(s.WaitForNodeReady(s.WorkerNode(0), client))

	files := imageFiles{
		runner: s.buildRunner(),
		data:   deterministicFiles(),
	}
	changed := cloneFiles(files)
	changed.data[changedFile] = deterministicBytes(1007, fileSize)

	treeA := s.serveTree("a", firstSyncPort, files)
	treeB := s.serveTree("b", secondSyncPort, changed)
	s.NotEqual(treeA, treeB)

	repository := s.registryRepository()
	imageA := s.pushImage(repository, "liquid-a", files, treeA, syncURL(firstSyncPort))
	imageB := s.pushImage(repository, "liquid-b", changed, treeB, syncURL(secondSyncPort))
	stockImage := s.pushImage(repository, "stock", files, "", "")
	unavailableTree := "blake3:" + strings.Repeat("0", 64)
	fallbackImage := s.pushImage(repository, "fallback", files, unavailableTree, "http://127.0.0.1:19999")

	s.runPod(client, "liquid-a", imageA)
	metricsAfterA := s.metrics()
	firstBytes := metricValue(s.T(), metricsAfterA, "liquid_node_store_bytes_fetched_total", treeA)
	s.GreaterOrEqual(firstBytes, uint64(fileCount*fileSize))

	s.runPod(client, "liquid-b", imageB)
	metricsAfterB := s.metrics()
	secondChunks := metricValue(s.T(), metricsAfterB, "liquid_node_store_chunks_fetched_total", treeB)
	secondBytes := metricValue(s.T(), metricsAfterB, "liquid_node_store_bytes_fetched_total", treeB)
	secondAvoided := metricValue(s.T(), metricsAfterB, "liquid_node_store_bytes_avoided_total", treeB)
	s.LessOrEqual(secondBytes, uint64(fileSize+(128<<10)))
	s.GreaterOrEqual(secondAvoided, uint64((fileCount-1)*fileSize))
	s.T().Logf("pull=liquid-a tree=%s chunks=%d bytes=%d", treeA, metricValue(s.T(), metricsAfterB, "liquid_node_store_chunks_fetched_total", treeA), firstBytes)
	s.T().Logf("pull=liquid-b tree=%s chunks=%d bytes=%d avoided=%d", treeB, secondChunks, secondBytes, secondAvoided)

	s.runPod(client, "stock", stockImage)
	s.runPod(client, "fallback", fallbackImage)
}

func (s *nodeStoreSuite) configureContainerdRegistry() {
	worker := s.WorkerNode(0)
	registry := fmt.Sprintf("%s:%d", s.RegistryNode(), s.RegistryPort())
	directory := "/etc/k0s/containerd.d/certs.d/" + registry
	s.MakeDir(worker, directory)
	s.PutFile(worker, "/etc/k0s/containerd.d/registry.toml", `version = 3
[plugins."io.containerd.cri.v1.images".registry]
  config_path = "/etc/k0s/containerd.d/certs.d"
`)
	s.PutFile(worker, directory+"/hosts.toml", fmt.Sprintf(`server = "http://%s"
[host."http://%s"]
  capabilities = ["pull", "resolve"]
`, registry, registry))
}

func (s *nodeStoreSuite) initializeCluster() {
	clusterConfig := k0sv1beta1.DefaultClusterConfig()
	clusterConfig.Spec.Network.Provider = "kuberouter"
	for _, component := range clusterConfig.Spec.Liquid.Components.Entries() {
		component.Component.Enabled = false
	}
	configData, err := yaml.Marshal(clusterConfig)
	s.Require().NoError(err)
	s.WriteFileContent(s.ControllerNode(0), "/tmp/k0s.yaml", configData)
	s.Require().NoError(s.InitController(0, "--config=/tmp/k0s.yaml"))
	s.Require().NoError(s.RunWorkers())
}

func (s *nodeStoreSuite) buildRunner() []byte {
	output := filepath.Join(s.T().TempDir(), "runner")
	command := exec.CommandContext(s.Context(), "go", "build", "-trimpath", "-o", output, "./testdata/runner")
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	combined, err := command.CombinedOutput()
	s.Require().NoErrorf(err, "build runner: %s", combined)
	data, err := os.ReadFile(output)
	s.Require().NoError(err)
	return data
}

func (s *nodeStoreSuite) serveTree(name string, port int, files imageFiles) string {
	worker := s.WorkerNode(0)
	project := "/tmp/liquid-image-" + name
	cache := "/tmp/liquid-cache-" + name
	_, err := s.RunCommandWorker(0, "mkdir -p "+project+"/data "+cache)
	s.Require().NoError(err)
	s.WriteFileContent(worker, project+"/runner", files.runner)
	for index, data := range files.data {
		s.WriteFileContent(worker, fmt.Sprintf("%s/data/file-%02d.bin", project, index), data)
	}
	command := fmt.Sprintf(
		"chmod 0755 %[1]s/runner && cd %[1]s && LIQUID_WRAP_CACHE=%[2]s %[3]s wrap --no-verify /bin/true >/tmp/liquid-wrap-%[4]s.log && LIQUID_WRAP_CACHE=%[2]s %[3]s build --plan | sed -n 's/.*\"inputs\":[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p' | head -n 1",
		project,
		cache,
		liquidExecutable,
		name,
	)
	tree, err := s.RunCommandWorker(0, command)
	s.Require().NoError(err)
	tree = strings.TrimSpace(tree)
	s.Require().True(strings.HasPrefix(tree, "blake3:"), "unexpected liquid tree digest %q", tree)

	serveCommand := fmt.Sprintf(
		"nohup %s serve sync %s/repository --listen 127.0.0.1 --port %d >/tmp/liquid-sync-%s.log 2>&1 &",
		liquidExecutable,
		cache,
		port,
		name,
	)
	_, err = s.RunCommandWorker(0, serveCommand)
	s.Require().NoError(err)
	waitCommand := fmt.Sprintf(
		"i=0; until wget -qO /dev/null 'http://127.0.0.1:%d/sync/chunks/%s?offset=0&length=1'; do i=$((i+1)); test $i -lt 50; sleep 0.1; done",
		port,
		tree,
	)
	_, err = s.RunCommandWorker(0, waitCommand)
	s.Require().NoError(err)
	return tree
}

func (s *nodeStoreSuite) registryRepository() *remote.Repository {
	reference := fmt.Sprintf("localhost:%d/liquid-node", s.GetRegistryHostPort())
	repository, err := remote.NewRepository(reference)
	s.Require().NoError(err)
	repository.PlainHTTP = true
	return repository
}

func (s *nodeStoreSuite) pushImage(repository *remote.Repository, tag string, files imageFiles, tree, storeURL string) string {
	layer, diffID := layerBytes(s.T(), files, tag)
	layerDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayerGzip, layer)
	s.pushBlob(repository, layerDescriptor, layer)

	labels := map[string]string{}
	if storeURL != "" {
		labels["io.liquid.store"] = storeURL
	}
	config := ocispec.Image{
		Platform: ocispec.Platform{Architecture: runtime.GOARCH, OS: "linux"},
		Config: ocispec.ImageConfig{
			Entrypoint: []string{"/runner"},
			Labels:     labels,
		},
		RootFS: ocispec.RootFS{
			Type:    "layers",
			DiffIDs: []digest.Digest{diffID},
		},
	}
	configBytes, err := json.Marshal(config)
	s.Require().NoError(err)
	configDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageConfig, configBytes)
	if storeURL != "" {
		configDescriptor.Annotations = map[string]string{"io.liquid.store": storeURL}
	}
	s.pushBlob(repository, configDescriptor, configBytes)

	annotations := map[string]string{}
	if tree != "" {
		annotations["io.liquid.tree"] = tree
	}
	manifest := ocispec.Manifest{
		Versioned:   imagespecs.Versioned{SchemaVersion: 2},
		MediaType:   ocispec.MediaTypeImageManifest,
		Config:      configDescriptor,
		Layers:      []ocispec.Descriptor{layerDescriptor},
		Annotations: annotations,
	}
	manifestBytes, err := json.Marshal(manifest)
	s.Require().NoError(err)
	manifestDescriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestBytes)
	s.pushBlob(repository, manifestDescriptor, manifestBytes)
	s.Require().NoError(repository.Tag(s.Context(), manifestDescriptor, tag))
	return fmt.Sprintf("%s:%d/liquid-node:%s", s.RegistryNode(), s.RegistryPort(), tag)
}

func (s *nodeStoreSuite) pushBlob(repository *remote.Repository, descriptor ocispec.Descriptor, data []byte) {
	exists, err := repository.Exists(s.Context(), descriptor)
	s.Require().NoError(err)
	if exists {
		return
	}
	s.Require().NoError(repository.Push(s.Context(), descriptor, bytes.NewReader(data)))
}

func (s *nodeStoreSuite) runPod(client kubernetes.Interface, name, image string) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{
			NodeName:      s.WorkerNode(0),
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:            "reader",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
			}},
		},
	}
	_, err := client.CoreV1().Pods(metav1.NamespaceDefault).Create(s.Context(), pod, metav1.CreateOptions{})
	s.Require().NoError(err)
	err = wait.PollUntilContextTimeout(s.Context(), 500*time.Millisecond, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := client.CoreV1().Pods(metav1.NamespaceDefault).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		switch current.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, fmt.Errorf("pod %s failed: %s", name, current.Status.Message)
		default:
			return false, nil
		}
	})
	s.Require().NoError(err)
	logs, err := client.CoreV1().Pods(metav1.NamespaceDefault).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(s.Context())
	s.Require().NoError(err)
	s.Equal(fmt.Sprintf("files=%d bytes=%d\n", fileCount, fileCount*fileSize), string(logs))
}

func (s *nodeStoreSuite) metrics() string {
	output, err := s.RunCommandWorker(0, "wget -qO- http://127.0.0.1:8082/metrics")
	s.Require().NoError(err)
	return output
}

func metricValue(t *testing.T, metrics, name, tree string) uint64 {
	t.Helper()
	prefix := name + `{tree="` + tree + `"} `
	for line := range strings.SplitSeq(metrics, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 10, 64)
		if err != nil {
			t.Fatalf("parse metric %s: %v", line, err)
		}
		return value
	}
	t.Fatalf("metric %s for tree %s not found in:\n%s", name, tree, metrics)
	return 0
}

func layerBytes(t *testing.T, files imageFiles, identity string) ([]byte, digest.Digest) {
	t.Helper()
	var tarData bytes.Buffer
	writer := tar.NewWriter(&tarData)
	writeTarFile(t, writer, "runner", 0755, files.runner)
	writeTarFile(t, writer, ".image-identity", 0644, []byte(identity))
	for index, data := range files.data {
		writeTarFile(t, writer, fmt.Sprintf("data/file-%02d.bin", index), 0644, data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	if _, err := io.Copy(gzipWriter, bytes.NewReader(tarData.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes(), digest.FromBytes(tarData.Bytes())
}

func writeTarFile(t *testing.T, writer *tar.Writer, name string, mode int64, data []byte) {
	t.Helper()
	header := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		ModTime:  time.Unix(0, 0),
		Typeflag: tar.TypeReg,
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
}

func deterministicFiles() [][]byte {
	files := make([][]byte, fileCount)
	for index := range files {
		files[index] = deterministicBytes(uint64(index+1), fileSize)
	}
	return files
}

func deterministicBytes(seed uint64, size int) []byte {
	value := seed + 0x9e3779b97f4a7c15
	data := make([]byte, size)
	for index := range data {
		value ^= value << 13
		value ^= value >> 7
		value ^= value << 17
		data[index] = byte(value)
	}
	return data
}

func cloneFiles(original imageFiles) imageFiles {
	cloned := imageFiles{
		runner: append([]byte(nil), original.runner...),
		data:   make([][]byte, len(original.data)),
	}
	for index := range original.data {
		cloned.data[index] = append([]byte(nil), original.data[index]...)
	}
	return cloned
}

func syncURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func TestNodeStoreSuite(t *testing.T) {
	suite.Run(t, &nodeStoreSuite{BootlooseSuite: common.BootlooseSuite{
		ControllerCount: 1,
		WorkerCount:     1,
		WithRegistry:    true,
	}})
}
