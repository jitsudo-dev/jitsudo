// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestRequestCmd_DurationDefault(t *testing.T) {
	cmd := newRequestCmd()
	f := cmd.Flags().Lookup("duration")
	if f == nil {
		t.Fatal("--duration flag not defined")
	}
	if f.DefValue != "1h" {
		t.Errorf("default --duration = %q, want %q", f.DefValue, "1h")
	}
}

func TestRequestCmd_DurationNotRequired(t *testing.T) {
	cmd := newRequestCmd()
	f := cmd.Flags().Lookup("duration")
	if f == nil {
		t.Fatal("--duration flag not defined")
	}
	_, isRequired := f.Annotations[cobra.BashCompOneRequiredFlag]
	if isRequired {
		t.Error("--duration should not be marked required (it has a default of 1h)")
	}
}

func TestRequestCmd_ReasonNotRequired(t *testing.T) {
	cmd := newRequestCmd()
	f := cmd.Flags().Lookup("reason")
	if f == nil {
		t.Fatal("--reason flag not defined")
	}
	_, isRequired := f.Annotations[cobra.BashCompOneRequiredFlag]
	if isRequired {
		t.Error("--reason should not be marked required (enforcement belongs in OPA policy)")
	}
}

func TestRequestCmd_CoreFlagsStillRequired(t *testing.T) {
	cmd := newRequestCmd()
	for _, name := range []string{"provider", "role", "scope"} {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("--%s flag not defined", name)
		}
		_, isRequired := f.Annotations[cobra.BashCompOneRequiredFlag]
		if !isRequired {
			t.Errorf("--%s should be marked required", name)
		}
	}
}
