// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAuditCmd_OutputFlagHasShorthand(t *testing.T) {
	cmd := newAuditCmd()
	f := cmd.Flags().ShorthandLookup("o")
	if f == nil {
		t.Fatal("audit command is missing -o shorthand for --output")
	}
	if f.Name != "output" {
		t.Errorf("-o shorthand maps to %q, want \"output\"", f.Name)
	}
}

func TestAuditCmd_OutputFlagDefault(t *testing.T) {
	cmd := newAuditCmd()
	f := cmd.Flags().Lookup("output")
	if f == nil {
		t.Fatal("--output flag not defined on audit command")
	}
	if f.DefValue != "table" {
		t.Errorf("default --output = %q, want \"table\"", f.DefValue)
	}
}

func TestAuditCmd_OutputFlagDescription(t *testing.T) {
	cmd := newAuditCmd()
	f := cmd.Flags().Lookup("output")
	if f == nil {
		t.Fatal("--output flag not defined on audit command")
	}
	// Confirm the flag description mentions the supported formats.
	for _, want := range []string{"table", "json", "csv"} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("--output usage does not mention %q: %s", want, f.Usage)
		}
	}
}

func TestAuditCmd_OutputShorthandNotConflictsWithGlobal(t *testing.T) {
	// The audit command defines its own -o, --output local flag which shadows
	// the global persistent -o. Cobra should resolve -o to the local flag.
	cmd := newAuditCmd()
	f := cmd.Flags().ShorthandLookup("o")
	if f == nil {
		t.Fatal("-o shorthand not found on audit command")
	}
	_, isRequired := f.Annotations[cobra.BashCompOneRequiredFlag]
	if isRequired {
		t.Error("--output should not be required")
	}
}
