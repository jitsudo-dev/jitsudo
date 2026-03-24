// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
)

func newRequestCmd() *cobra.Command {
	var (
		provider   string
		role       string
		scope      string
		duration   string
		reason     string
		breakGlass bool
		wait       bool
	)

	cmd := &cobra.Command{
		Use:   "request",
		Short: "Submit a new elevation request",
		Long: `Submit a request for temporary elevated cloud permissions.

The request enters an approval workflow defined by your organization's policies.
Upon approval, credentials are issued for the specified duration and automatically revoked on expiry.`,
		Example: `  jitsudo request \
    --provider aws \
    --role prod-infra-admin \
    --scope 123456789012 \
    --duration 2h \
    --reason "Investigating P1 ECS crash - INC-4421"

  jitsudo request --provider gcp --role roles/editor --scope my-project --duration 1h --reason "Deploy hotfix"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_ = wait // polling deferred to Milestone 2

			d, err := time.ParseDuration(duration)
			if err != nil {
				return fmt.Errorf("invalid --duration %q: %w", duration, err)
			}

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
				Provider:        provider,
				Role:            role,
				ResourceScope:   scope,
				DurationSeconds: int64(d.Seconds()),
				Reason:          reason,
				BreakGlass:      breakGlass,
			})
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}

			req := resp.GetRequest()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Request ID: %s\nState:      %s\n", req.GetId(), stateString(req.GetState()))
			if !flags.quiet {
				fmt.Fprintf(out, "Provider:   %s\nRole:       %s\nScope:      %s\n",
					req.GetProvider(), req.GetRole(), req.GetResourceScope())
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "Cloud provider: aws, azure, gcp, kubernetes (required)")
	cmd.Flags().StringVar(&role, "role", "", "Role or permission set to request (required)")
	cmd.Flags().StringVar(&scope, "scope", "", "Resource scope: AWS account ID, GCP project, K8s namespace (required)")
	cmd.Flags().StringVar(&duration, "duration", "1h", "Elevation duration, e.g. 1h, 30m (default: 1h)")
	cmd.Flags().StringVar(&reason, "reason", "", "Justification for the request")
	cmd.Flags().BoolVar(&breakGlass, "break-glass", false, "Emergency break-glass mode: bypass approval with immediate alerts")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until the request is approved or denied")

	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("role")
	_ = cmd.MarkFlagRequired("scope")

	return cmd
}
