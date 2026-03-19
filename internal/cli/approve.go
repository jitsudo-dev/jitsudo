package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newApproveCmd() *cobra.Command {
	var comment string

	cmd := &cobra.Command{
		Use:   "approve <request-id>",
		Short: "Approve a pending elevation request",
		Args:  cobra.ExactArgs(1),
		Example: `  jitsudo approve req_01J8KZ...
  jitsudo approve req_01J8KZ... --comment "Approved for INC-4421 response"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: submit approval to control plane
			fmt.Fprintln(cmd.OutOrStdout(), "approve: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&comment, "comment", "", "Optional approval comment")

	return cmd
}

func newDenyCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "deny <request-id>",
		Short: "Deny a pending elevation request",
		Args:  cobra.ExactArgs(1),
		Example: `  jitsudo deny req_01J8KZ... --reason "Not authorized for production access"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: submit denial to control plane
			fmt.Fprintln(cmd.OutOrStdout(), "deny: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for denial (required)")
	_ = cmd.MarkFlagRequired("reason")

	return cmd
}
