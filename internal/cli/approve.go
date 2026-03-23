// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
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
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{
				RequestId: args[0],
				Comment:   comment,
			})
			if err != nil {
				return fmt.Errorf("approve: %w", err)
			}

			req := resp.GetRequest()
			fmt.Fprintf(cmd.OutOrStdout(), "Request %s → %s\n", req.GetId(), stateString(req.GetState()))
			return nil
		},
	}

	cmd.Flags().StringVar(&comment, "comment", "", "Optional approval comment")

	return cmd
}

func newDenyCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:     "deny <request-id>",
		Short:   "Deny a pending elevation request",
		Args:    cobra.ExactArgs(1),
		Example: `  jitsudo deny req_01J8KZ... --reason "Not authorized for production access"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().DenyRequest(ctx, &jitsudov1alpha1.DenyRequestInput{
				RequestId: args[0],
				Reason:    reason,
			})
			if err != nil {
				return fmt.Errorf("deny: %w", err)
			}

			req := resp.GetRequest()
			fmt.Fprintf(cmd.OutOrStdout(), "Request %s → %s\n", req.GetId(), stateString(req.GetState()))
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for denial (required)")
	_ = cmd.MarkFlagRequired("reason")

	return cmd
}
