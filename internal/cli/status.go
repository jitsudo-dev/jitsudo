// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
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
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			out := cmd.OutOrStdout()

			if len(args) > 0 {
				resp, err := c.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{
					Id: args[0],
				})
				if err != nil {
					return fmt.Errorf("get request: %w", friendlyError(err))
				}
				printRequest(out, resp.GetRequest())
				return nil
			}

			filter := &jitsudov1alpha1.ListRequestsFilter{
				Mine:    mine,
				Pending: pending,
			}
			resp, err := c.Service().ListRequests(ctx, filter)
			if err != nil {
				return fmt.Errorf("list requests: %w", friendlyError(err))
			}

			requests := resp.GetRequests()
			if len(requests) == 0 {
				fmt.Fprintln(out, "No requests found.")
				return nil
			}

			fmt.Fprintf(out, "%-28s %-10s %-25s %s\n", "ID", "STATE", "REQUESTER", "REASON")
			fmt.Fprintln(out, strings.Repeat("-", 85))
			for _, r := range requests {
				fmt.Fprintf(out, "%-28s %-10s %-25s %s\n",
					r.GetId(), stateString(r.GetState()), r.GetRequesterIdentity(), r.GetReason())
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&mine, "mine", false, "List all requests submitted by the current user")
	cmd.Flags().BoolVar(&pending, "pending", false, "List all pending requests (approvers)")

	return cmd
}

func printRequest(out io.Writer, r *jitsudov1alpha1.ElevationRequest) {
	fmt.Fprintf(out, "ID:        %s\n", r.GetId())
	fmt.Fprintf(out, "State:     %s\n", stateString(r.GetState()))
	fmt.Fprintf(out, "Requester: %s\n", r.GetRequesterIdentity())
	fmt.Fprintf(out, "Provider:  %s\n", r.GetProvider())
	fmt.Fprintf(out, "Role:      %s\n", r.GetRole())
	fmt.Fprintf(out, "Scope:     %s\n", r.GetResourceScope())
	fmt.Fprintf(out, "Duration:  %s\n", (time.Duration(r.GetDurationSeconds()) * time.Second).String())
	fmt.Fprintf(out, "Reason:    %s\n", r.GetReason())
	if r.GetApproverIdentity() != "" {
		fmt.Fprintf(out, "Approver:  %s\n", r.GetApproverIdentity())
	}
	if r.GetApproverComment() != "" {
		fmt.Fprintf(out, "Comment:   %s\n", r.GetApproverComment())
	}
	if r.GetExpiresAt() != nil {
		fmt.Fprintf(out, "Expires:   %s\n", r.GetExpiresAt().AsTime().Format(time.RFC3339))
	}
}
