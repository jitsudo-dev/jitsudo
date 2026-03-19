package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRevokeCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "revoke <request-id>",
		Short: "Revoke an active elevation before its natural expiry",
		Example: `  jitsudo revoke req_01J8KZ...
  jitsudo revoke req_01J8KZ... --reason "Incident resolved, access no longer needed"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: submit revocation to control plane
			fmt.Fprintln(cmd.OutOrStdout(), "revoke: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Optional reason for early revocation")

	return cmd
}
