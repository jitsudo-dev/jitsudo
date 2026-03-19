package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <request-id> -- <command> [args...]",
		Short: "Execute a command with elevated credentials injected as environment variables",
		Long: `Execute a single command in a subprocess with elevated credentials injected into
its environment. The parent shell never receives the credentials.

The subprocess inherits all environment variables from the parent, plus the
provider-specific credential variables (e.g., AWS_ACCESS_KEY_ID for AWS).`,
		Example: `  jitsudo exec req_01J8KZ... -- aws ecs describe-tasks --cluster prod --tasks abc123
  jitsudo exec req_01J8KZ... -- kubectl get pods -n production`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: fetch credentials, inject into subprocess environment, exec
			fmt.Fprintln(cmd.OutOrStdout(), "exec: not yet implemented")
			return nil
		},
	}

	return cmd
}
