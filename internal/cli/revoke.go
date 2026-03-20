package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
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
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().RevokeRequest(ctx, &jitsudov1alpha1.RevokeRequestInput{
				RequestId: args[0],
				Reason:    reason,
			})
			if err != nil {
				return fmt.Errorf("revoke: %w", err)
			}

			req := resp.GetRequest()
			fmt.Fprintf(cmd.OutOrStdout(), "Request %s → %s\n", req.GetId(), stateString(req.GetState()))
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Optional reason for early revocation")

	return cmd
}
