// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/k0sproject/k0s/pkg/liquidadmission"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/spf13/cobra"
)

type admissionAdapterOptions struct {
	listen  string
	liquid  string
	control string
	routes  string
	tlsCert string
	tlsKey  string
	tlsCA   string
	tlsName string
}

func newAdmissionAdapterCmd() *cobra.Command {
	options := admissionAdapterOptions{}
	command := &cobra.Command{
		Use:    "admission-adapter",
		Short:  "Route AdmissionReviews to liquid policies and emit decision events",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runAdmissionAdapter(command.Context(), options)
		},
	}
	command.Flags().StringVar(&options.listen, "listen", ":9443", "HTTPS listen address")
	command.Flags().StringVar(&options.liquid, "liquid", "https://127.0.0.1:8443", "liquid admission address")
	command.Flags().StringVar(&options.control, "control", "https://127.0.0.1:8083", "liquid admission control address")
	command.Flags().StringVar(&options.routes, "routes", "/etc/liquid/policies/routes.json", "policy route file")
	command.Flags().StringVar(&options.tlsCert, "tls-cert", "/etc/liquid/tls/tls.crt", "serving certificate")
	command.Flags().StringVar(&options.tlsKey, "tls-key", "/etc/liquid/tls/tls.key", "serving key")
	command.Flags().StringVar(&options.tlsCA, "tls-ca", "/etc/liquid/tls/ca.crt", "liquid serving CA")
	command.Flags().StringVar(&options.tlsName, "tls-server-name", "liquid-admission.kube-system.svc", "liquid serving certificate name")
	return command
}

func runAdmissionAdapter(ctx context.Context, options admissionAdapterOptions) error {
	routesData, err := os.ReadFile(options.routes)
	if err != nil {
		return fmt.Errorf("read admission routes: %w", err)
	}
	var routes liquidadmission.Routes
	if err := json.Unmarshal(routesData, &routes); err != nil {
		return fmt.Errorf("decode admission routes: %w", err)
	}
	client, err := admissionHTTPClient(options.tlsCA, options.tlsName)
	if err != nil {
		return err
	}
	adapter, err := liquidadmission.NewAdapter(options.liquid, routes, client)
	if err != nil {
		return err
	}
	kube, err := inClusterClient()
	if err != nil {
		return err
	}
	go consumeAdmissionDecisions(ctx, client, options.control+"/v1/decisions", adapter, eventSink{kube: kube})
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := reloadAdmissionRoutes(options.routes, adapter); err != nil {
			http.Error(response, "admission policy routes unavailable", http.StatusServiceUnavailable)
			return
		}
		adapter.ServeHTTP(response, request)
	})
	server := &http.Server{Addr: options.listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	if err := server.ListenAndServeTLS(options.tlsCert, options.tlsKey); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve admission adapter: %w", err)
	}
	return nil
}

func reloadAdmissionRoutes(path string, adapter *liquidadmission.Adapter) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var routes liquidadmission.Routes
	if err := json.Unmarshal(data, &routes); err != nil {
		return err
	}
	return adapter.ReplaceRoutes(routes)
}

func admissionHTTPClient(caPath, serverName string) (*http.Client, error) {
	caData, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read liquid serving CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return nil, errors.New("parse liquid serving CA")
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: serverName}}}, nil
}

func inClusterClient() (kubernetes.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster configuration: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create in-cluster client: %w", err)
	}
	return client, nil
}

func consumeAdmissionDecisions(ctx context.Context, client *http.Client, endpoint string, adapter *liquidadmission.Adapter, sink liquidadmission.DecisionSink) {
	for ctx.Err() == nil {
		if err := liquidadmission.ConsumeDecisions(ctx, client, endpoint, adapter, sink); err != nil && ctx.Err() == nil {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
		}
	}
}

type eventSink struct {
	kube kubernetes.Interface
}

func (s eventSink) Emit(ctx context.Context, namespace string, decision liquidadmission.Decision) error {
	reason := "LiquidAdmissionAllowed"
	eventType := corev1.EventTypeNormal
	if decision.Outcome != "allowed" {
		reason = "LiquidAdmissionDenied"
		eventType = corev1.EventTypeWarning
	}
	message := decision.Image
	if len(decision.Reasons) != 0 {
		message += ": " + decision.Reasons[0].Message
	}
	_, err := s.kube.CoreV1().Events(namespace).Create(ctx, &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{GenerateName: "liquid-admission."},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: namespace},
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source:         corev1.EventSource{Component: "k0s-liquid-admission"},
	}, metav1.CreateOptions{})
	return err
}
