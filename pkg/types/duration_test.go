// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		input   string
		wantStr string
		wantErr bool
	}{
		{"1h", "1h0m0s", false},
		{"30m", "30m0s", false},
		{"5s", "5s", false},
		{"0s", "0s", false},
		{"8h30m", "8h30m0s", false},
		{"not-a-duration", "", true},
		{"abc", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			yamlInput := "duration: " + tc.input
			type wrapper struct {
				Duration Duration `yaml:"duration"`
			}
			var w wrapper
			err := yaml.Unmarshal([]byte(yamlInput), &w)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if got := w.Duration.Duration.String(); got != tc.wantStr {
				t.Errorf("got %q, want %q", got, tc.wantStr)
			}
		})
	}
}

func TestDuration_MarshalYAML_RoundTrip(t *testing.T) {
	tests := []string{"1h0m0s", "30m0s", "5s", "0s", "8h30m0s"}

	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			// Parse the duration string into a Duration.
			yamlInput := "duration: " + s
			type wrapper struct {
				Duration Duration `yaml:"duration"`
			}
			var w wrapper
			if err := yaml.Unmarshal([]byte(yamlInput), &w); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			// Marshal back to YAML.
			out, err := yaml.Marshal(&w)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			// Unmarshal the marshalled output and compare.
			var w2 wrapper
			if err := yaml.Unmarshal(out, &w2); err != nil {
				t.Fatalf("round-trip unmarshal: %v", err)
			}

			if w.Duration.Duration != w2.Duration.Duration {
				t.Errorf("round-trip mismatch: got %v, want %v", w2.Duration.Duration, w.Duration.Duration)
			}
		})
	}
}

func TestDuration_MarshalYAML_ProducesString(t *testing.T) {
	// MarshalYAML should return a human-readable string, not nanoseconds.
	yamlInput := "duration: 2h"
	type wrapper struct {
		Duration Duration `yaml:"duration"`
	}
	var w wrapper
	if err := yaml.Unmarshal([]byte(yamlInput), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The marshalled output should contain "2h" not a large integer.
	s := string(out)
	if len(s) == 0 {
		t.Fatal("empty marshal output")
	}
	// Verify we get the string form "2h0m0s" in the output.
	if !containsSubstr(s, "2h") {
		t.Errorf("expected marshalled output to contain '2h', got: %q", s)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
