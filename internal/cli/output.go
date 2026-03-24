// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"io"
	"time"

	"gopkg.in/yaml.v3"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
)

// requestRow is the JSON/YAML representation of an ElevationRequest.
type requestRow struct {
	ID               string `json:"id" yaml:"id"`
	State            string `json:"state" yaml:"state"`
	RequesterIdentity string `json:"requester_identity" yaml:"requester_identity"`
	Provider         string `json:"provider" yaml:"provider"`
	Role             string `json:"role" yaml:"role"`
	ResourceScope    string `json:"resource_scope" yaml:"resource_scope"`
	DurationSeconds  int64  `json:"duration_seconds" yaml:"duration_seconds"`
	Reason           string `json:"reason" yaml:"reason"`
	ApproverIdentity string `json:"approver_identity,omitempty" yaml:"approver_identity,omitempty"`
	ApproverComment  string `json:"approver_comment,omitempty" yaml:"approver_comment,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
}

func requestToRow(r *jitsudov1alpha1.ElevationRequest) requestRow {
	row := requestRow{
		ID:               r.GetId(),
		State:            stateString(r.GetState()),
		RequesterIdentity: r.GetRequesterIdentity(),
		Provider:         r.GetProvider(),
		Role:             r.GetRole(),
		ResourceScope:    r.GetResourceScope(),
		DurationSeconds:  r.GetDurationSeconds(),
		Reason:           r.GetReason(),
		ApproverIdentity: r.GetApproverIdentity(),
		ApproverComment:  r.GetApproverComment(),
	}
	if r.GetExpiresAt() != nil {
		row.ExpiresAt = r.GetExpiresAt().AsTime().UTC().Format(time.RFC3339)
	}
	return row
}

// policyRow is the JSON/YAML representation of a Policy.
type policyRow struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	UpdatedAt   string `json:"updated_at" yaml:"updated_at"`
	Rego        string `json:"rego,omitempty" yaml:"rego,omitempty"`
}

func policyToRow(p *jitsudov1alpha1.Policy, includeRego bool) policyRow {
	row := policyRow{
		ID:          p.GetId(),
		Name:        p.GetName(),
		Type:        policyTypeString(p.GetType()),
		Enabled:     p.GetEnabled(),
		Description: p.GetDescription(),
		UpdatedAt:   p.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339),
	}
	if includeRego {
		row.Rego = p.GetRego()
	}
	return row
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func encodeYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc.Encode(v)
}
