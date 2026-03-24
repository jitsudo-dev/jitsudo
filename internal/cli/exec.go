// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <request-id> -- <command> [args...]",
		Short: "Execute a command with elevated credentials injected as environment variables",
		Long: `Execute a single command in a subprocess with elevated credentials injected into
its environment. The parent shell never receives the credentials.

The subprocess inherits all environment variables from the parent, plus the
provider-specific credential variables (e.g., MOCK_ACCESS_KEY for the mock provider).`,
		Example: `  jitsudo exec req_01J8KZ... -- aws ecs describe-tasks --cluster prod --tasks abc123
  jitsudo exec req_01J8KZ... -- kubectl get pods -n production
  jitsudo exec req_01J8KZ... -- env | grep MOCK_`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			requestID := args[0]
			cmdName := args[1]
			cmdArgs := args[2:]

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{
				RequestId: requestID,
			})
			if err != nil {
				return fmt.Errorf("get credentials: %w", err)
			}

			// Build environment: inherit parent env + inject credentials.
			env := os.Environ()
			for _, cred := range resp.GetGrant().GetCredentials() {
				env = append(env, cred.GetName()+"="+cred.GetValue())
			}

			proc := exec.CommandContext(ctx, cmdName, cmdArgs...)
			proc.Env = env
			proc.Stdin = cmd.InOrStdin()
			proc.Stdout = cmd.OutOrStdout()
			proc.Stderr = cmd.ErrOrStderr()

			if err := proc.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				return err
			}
			return nil
		},
	}

	return cmd
}
