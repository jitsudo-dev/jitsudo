// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
)

// TestOutputFlag_GlobalFlagExists verifies the persistent -o/--output flag is
// registered on the root command and has the expected default.
func TestOutputFlag_GlobalFlagExists(t *testing.T) {
	root := NewRootCmd()
	f := root.PersistentFlags().Lookup("output")
	if f == nil {
		t.Fatal("--output flag not defined on root command")
	}
	if f.DefValue != "table" {
		t.Errorf("default --output = %q, want %q", f.DefValue, "table")
	}
	if f.Shorthand != "o" {
		t.Errorf("--output shorthand = %q, want %q", f.Shorthand, "o")
	}
}

// TestEncodeJSON verifies encodeJSON produces valid JSON for a struct.
func TestEncodeJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeJSON(&buf, map[string]string{"key": "value"}); err != nil {
		t.Fatalf("encodeJSON: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if got["key"] != "value" {
		t.Errorf("got %q, want %q", got["key"], "value")
	}
}

// TestEncodeYAML verifies encodeYAML produces valid YAML for a struct.
func TestEncodeYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeYAML(&buf, map[string]string{"key": "value"}); err != nil {
		t.Fatalf("encodeYAML: %v", err)
	}
	var got map[string]string
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid YAML: %v\noutput: %s", err, buf.String())
	}
	if got["key"] != "value" {
		t.Errorf("got %q, want %q", got["key"], "value")
	}
}

// TestRequestToRow verifies requestToRow maps proto fields to the JSON row.
func TestRequestToRow(t *testing.T) {
	r := &jitsudov1alpha1.ElevationRequest{
		Id:                "req_01",
		State:             jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING,
		RequesterIdentity: "alice@example.com",
		Provider:          "mock",
		Role:              "test-role",
		ResourceScope:     "test-scope",
		DurationSeconds:   3600,
		Reason:            "testing",
	}
	row := requestToRow(r)
	if row.ID != "req_01" {
		t.Errorf("ID: got %q", row.ID)
	}
	if row.State != "PENDING" {
		t.Errorf("State: got %q", row.State)
	}
	if row.Provider != "mock" {
		t.Errorf("Provider: got %q", row.Provider)
	}
	if row.DurationSeconds != 3600 {
		t.Errorf("DurationSeconds: got %d", row.DurationSeconds)
	}
	if row.ExpiresAt != "" {
		t.Errorf("ExpiresAt should be empty when nil; got %q", row.ExpiresAt)
	}
}

// TestRequestToRow_WithExpiry verifies ExpiresAt is set when the proto field is non-nil.
func TestRequestToRow_WithExpiry(t *testing.T) {
	ts := timestamppb.Now()
	r := &jitsudov1alpha1.ElevationRequest{ExpiresAt: ts}
	row := requestToRow(r)
	if row.ExpiresAt == "" {
		t.Error("ExpiresAt should be non-empty when set")
	}
}

// TestPolicyToRow_WithoutRego verifies Rego is omitted when includeRego=false.
func TestPolicyToRow_WithoutRego(t *testing.T) {
	p := &jitsudov1alpha1.Policy{
		Id:      "pol_01",
		Name:    "test-policy",
		Type:    jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY,
		Enabled: true,
		Rego:    "package test\nallow = true",
	}
	row := policyToRow(p, false)
	if row.Rego != "" {
		t.Errorf("expected empty Rego when includeRego=false; got %q", row.Rego)
	}
}

// TestPolicyToRow_WithRego verifies Rego is included when includeRego=true.
func TestPolicyToRow_WithRego(t *testing.T) {
	p := &jitsudov1alpha1.Policy{
		Id:   "pol_01",
		Name: "test-policy",
		Rego: "package test\nallow = true",
	}
	row := policyToRow(p, true)
	if !strings.Contains(row.Rego, "allow") {
		t.Errorf("expected Rego to be set; got %q", row.Rego)
	}
}
