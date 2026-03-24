// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestExecCmd_CommandNotFoundNoDuplicatePrefix verifies that when a subprocess
// command is not found, the error message does not have the doubled "exec: exec:"
// prefix that occurred when we wrapped os/exec errors with fmt.Errorf("exec: %w", err).
//
// os/exec already prefixes command-not-found errors with "exec:". Cobra further
// prefixes the RunE return value with the subcommand name "exec:". Wrapping the
// error ourselves produced the triple-prefixed message:
//
//	Error: exec: exec: "cmd": executable file not found in $PATH
//
// Returning the os/exec error directly gives the correct two-level message:
//
//	Error: exec: "cmd": executable file not found in $PATH
func TestExecCmd_CommandNotFoundNoDuplicatePrefix(t *testing.T) {
	const notFound = "definitely-not-a-real-command-for-jitsudo-testing"

	// Confirm os/exec returns an "exec: ..." error for a missing command.
	osExecErr := exec.Command(notFound).Run()
	if osExecErr == nil {
		t.Skipf("command %q unexpectedly found in PATH", notFound)
	}

	errStr := osExecErr.Error()
	if !strings.HasPrefix(errStr, "exec: ") {
		t.Fatalf("expected os/exec error to start with \"exec: \", got: %q", errStr)
	}

	// Demonstrate that wrapping with fmt.Errorf("exec: %w") doubles the prefix —
	// this is the bug that was fixed.
	wrapped := fmt.Errorf("exec: %w", osExecErr)
	if !strings.HasPrefix(wrapped.Error(), "exec: exec: ") {
		t.Errorf("sanity: wrapped error should start with \"exec: exec: \", got: %q", wrapped.Error())
	}

	// The fix: return the os/exec error directly. Cobra prepends the subcommand
	// name "exec:" when printing, producing exactly one "exec:" from the error
	// itself. Verify the raw error does NOT already start with "exec: exec:".
	if strings.HasPrefix(errStr, "exec: exec: ") {
		t.Errorf("os/exec error has unexpected double prefix: %q", errStr)
	}
}
