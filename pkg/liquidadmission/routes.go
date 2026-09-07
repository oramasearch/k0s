// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

// Package liquidadmission implements k0s's routing and event adapter.
package liquidadmission

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
)

// Routes maps a Pod namespace to a liquid named policy set.
type Routes struct {
	Version    int               `json:"v"`
	Default    string            `json:"default"`
	Namespaces map[string]string `json:"namespaces"`
}

// NamespacePolicy is the typed subset read from an AdmissionPolicy CRD.
type NamespacePolicy struct {
	Namespace string
	Name      string
	Spec      v1beta1.LiquidAdmission
}

// Resolve returns named liquid policies and a namespace route map.
func Resolve(defaultPolicy v1beta1.LiquidAdmission, policies []NamespacePolicy) (map[string]v1beta1.LiquidAdmission, Routes, error) {
	sets := map[string]v1beta1.LiquidAdmission{"default": defaultPolicy}
	routes := Routes{Version: 1, Default: "default", Namespaces: make(map[string]string, len(policies))}
	ordered := append([]NamespacePolicy(nil), policies...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Namespace == ordered[right].Namespace {
			return ordered[left].Name < ordered[right].Name
		}
		return ordered[left].Namespace < ordered[right].Namespace
	})
	for _, policy := range ordered {
		if policy.Namespace == "" || policy.Name == "" {
			return nil, Routes{}, errors.New("admission policy namespace and name are required")
		}
		if _, exists := routes.Namespaces[policy.Namespace]; exists {
			return nil, Routes{}, fmt.Errorf("multiple admission policies in namespace %q", policy.Namespace)
		}
		if errors := policy.Spec.Validate(nil); len(errors) != 0 {
			return nil, Routes{}, fmt.Errorf("admission policy %s/%s is invalid: %w", policy.Namespace, policy.Name, errors[0])
		}
		setName := policySetName(policy.Namespace, policy.Name)
		sets[setName] = policy.Spec
		routes.Namespaces[policy.Namespace] = setName
	}
	return sets, routes, nil
}

// PolicyJSON produces liquid's strict version-1 policy JSON.
func PolicyJSON(policy v1beta1.LiquidAdmission) ([]byte, error) {
	return json.Marshal(liquidPolicyJSON{
		Version:           1,
		Mode:              policy.Mode,
		Issuers:           nonNil(policy.Issuers),
		Profiles:          nonNil(policy.Profiles),
		Exempt:            nonNil(policy.Exempt),
		ExemptNamespaces:  []string{"kube-system"},
		AttestationSource: policy.AttestationStore,
		Log:               liquidLogJSON(policy.Log),
	})
}

type liquidPolicyJSON struct {
	Version           int             `json:"v"`
	Mode              string          `json:"mode"`
	Issuers           []string        `json:"issuers"`
	Profiles          []string        `json:"profiles"`
	Exempt            []string        `json:"exempt"`
	ExemptNamespaces  []string        `json:"exempt_namespaces"`
	AttestationSource string          `json:"attestation_source"`
	Log               liquidLogPolicy `json:"log"`
}

type liquidLogPolicy struct {
	Mode                    string         `json:"mode"`
	Lake                    string         `json:"lake"`
	Trust                   liquidLogTrust `json:"trust"`
	MaximumCheckpointAgeSec int            `json:"max_checkpoint_age_seconds"`
}

type liquidLogTrust struct {
	Identity         liquidLogIdentity  `json:"identity"`
	WitnessThreshold int                `json:"witness_threshold"`
	Witnesses        []liquidLogWitness `json:"witnesses"`
}

type liquidLogIdentity struct {
	TAAPID    string `json:"taap_id"`
	PublicKey string `json:"public_key"`
}

type liquidLogWitness struct {
	Endpoint  string `json:"endpoint"`
	PublicKey string `json:"public_key"`
}

func liquidLogJSON(log *v1beta1.LiquidAdmissionLog) liquidLogPolicy {
	output := liquidLogPolicy{
		Mode: v1beta1.LiquidAdmissionLogModeDeny,
		Trust: liquidLogTrust{
			Witnesses: []liquidLogWitness{},
		},
	}
	if log == nil {
		return output
	}
	if log.Mode != "" {
		output.Mode = log.Mode
	}
	output.Lake = log.Lake
	output.Trust.Identity = liquidLogIdentity{
		TAAPID:    log.Trust.Identity.TAAPID,
		PublicKey: log.Trust.Identity.PublicKey,
	}
	output.Trust.WitnessThreshold = log.Trust.WitnessThreshold
	output.Trust.Witnesses = make([]liquidLogWitness, 0, len(log.Trust.Witnesses))
	for _, witness := range log.Trust.Witnesses {
		output.Trust.Witnesses = append(output.Trust.Witnesses, liquidLogWitness{
			Endpoint:  witness.Endpoint,
			PublicKey: witness.PublicKey,
		})
	}
	output.MaximumCheckpointAgeSec = log.MaximumCheckpointAgeSeconds
	return output
}

func policySetName(namespace, name string) string {
	return strings.ReplaceAll(namespace+"--"+name, ".", "-")
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
