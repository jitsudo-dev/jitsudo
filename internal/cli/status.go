package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var (
		mine    bool
		pending bool
	)

	cmd := &cobra.Command{
		Use:   "status [request-id]",
		Short: "Check the status of an elevation request",
		Example: `  jitsudo status req_01J8KZ...
  jitsudo status --mine
  jitsudo status --pending`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: fetch and display request status from control plane
			fmt.Fprintln(cmd.OutOrStdout(), "status: not yet implemented")
			return nil
		},
	}

	cmd.Flags().BoolVar(&mine, "mine", false, "List all requests submitted by the current user")
	cmd.Flags().BoolVar(&pending, "pending", false, "List all pending requests (approvers)")

	return cmd
}
