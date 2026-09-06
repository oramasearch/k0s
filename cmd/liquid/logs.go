// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package liquid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/k0sproject/k0s/pkg/client/clientset"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/spf13/cobra"
)

const defaultLogLimit = 100

type logQueryOptions struct {
	limit int
	since time.Duration
}

type logEntry struct {
	timestamp time.Time
	container string
	line      string
}

type lokiQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func newLogsCmd() *cobra.Command {
	options := logQueryOptions{
		limit: defaultLogLimit,
		since: time.Hour,
	}
	cmd := &cobra.Command{
		Use:   "logs NAMESPACE/POD",
		Short: "Read pod logs from the liquid Loki endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, pod, err := parsePodReference(args[0])
			if err != nil {
				return err
			}
			if options.limit < 1 || options.limit > 5000 {
				return errors.New("limit must be between 1 and 5000")
			}
			if options.since < 0 {
				return errors.New("since must not be negative")
			}

			endpoint, err := liquidLogsEndpoint(cmd)
			if err != nil {
				return err
			}
			entries, err := queryLoki(
				cmd.Context(),
				&http.Client{Timeout: 30 * time.Second},
				endpoint,
				namespace,
				pod,
				options,
				time.Now(),
			)
			if err != nil {
				return err
			}
			return printLogEntries(cmd.OutOrStdout(), entries)
		},
	}

	cmd.Flags().IntVar(&options.limit, "limit", options.limit, "maximum number of log lines")
	cmd.Flags().DurationVar(&options.since, "since", options.since, "lookback duration")
	cmd.Flags().AddFlagSet(config.GetPersistentFlagSet())
	return cmd
}

func parsePodReference(reference string) (string, string, error) {
	namespace, pod, found := strings.Cut(reference, "/")
	if !found || strings.Contains(pod, "/") {
		return "", "", errors.New("pod reference must be NAMESPACE/POD")
	}
	if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
		return "", "", fmt.Errorf("invalid namespace %q: %s", namespace, problems[0])
	}
	if problems := validation.IsDNS1123Subdomain(pod); len(problems) != 0 {
		return "", "", fmt.Errorf("invalid pod %q: %s", pod, problems[0])
	}
	return namespace, pod, nil
}

func liquidLogsEndpoint(cmd *cobra.Command) (string, error) {
	opts, err := config.GetCmdOpts(cmd)
	if err != nil {
		return "", err
	}
	restConfig, err := kubernetes.ClientConfig(kubernetes.KubeconfigFromFile(opts.K0sVars.AdminKubeConfigPath))
	if err != nil {
		return "", fmt.Errorf("can't load the admin kubeconfig: %w", err)
	}
	clients, err := clientset.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("can't create the Kubernetes client: %w", err)
	}
	clusterConfig, err := clients.K0sV1beta1().ClusterConfigs(constant.ClusterConfigNamespace).Get(
		cmd.Context(),
		constant.ClusterConfigObjectName,
		metav1.GetOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("can't read the cluster configuration: %w", err)
	}
	if clusterConfig.Spec == nil || clusterConfig.Spec.Liquid == nil || clusterConfig.Spec.Liquid.Platform == nil {
		return "", errors.New("the cluster has no liquid logs endpoint")
	}
	return clusterConfig.Spec.Liquid.Platform.LogsEndpoint, nil
}

func queryLoki(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	namespace string,
	pod string,
	options logQueryOptions,
	now time.Time,
) ([]logEntry, error) {
	requestURL, err := url.Parse(strings.TrimRight(endpoint, "/") + "/loki/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("invalid logs endpoint: %w", err)
	}
	query := requestURL.Query()
	query.Set("query", fmt.Sprintf("{namespace=%q,pod=%q}", namespace, pod))
	query.Set("limit", strconv.Itoa(options.limit))
	query.Set("direction", "backward")
	if options.since > 0 {
		query.Set("start", strconv.FormatInt(now.Add(-options.since).UnixNano(), 10))
	}
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Loki request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query Loki: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("query Loki: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var result lokiQueryResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Loki response: %w", err)
	}
	if result.Status != "success" {
		return nil, fmt.Errorf("query Loki: response status %q", result.Status)
	}

	var entries []logEntry
	for _, stream := range result.Data.Result {
		for _, value := range stream.Values {
			if len(value) != 2 {
				return nil, errors.New("decode Loki response: log value must contain timestamp and line")
			}
			nanoseconds, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("decode Loki timestamp %q: %w", value[0], err)
			}
			entries = append(entries, logEntry{
				timestamp: time.Unix(0, nanoseconds).UTC(),
				container: stream.Stream["container"],
				line:      value[1],
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].timestamp.Before(entries[j].timestamp)
	})
	return entries, nil
}

func printLogEntries(output io.Writer, entries []logEntry) error {
	for _, entry := range entries {
		container := entry.container
		if container == "" {
			container = "-"
		}
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s\n", entry.timestamp.Format(time.RFC3339Nano), container, entry.line); err != nil {
			return err
		}
	}
	return nil
}
