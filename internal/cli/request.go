package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRequestCmd() *cobra.Command {
	var (
		provider  string
		role      string
		scope     string
		duration  string
		reason    string
		breakGlass bool
		wait      bool
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
			// TODO: validate flags, build request, submit to control plane
			fmt.Fprintln(cmd.OutOrStdout(), "request: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "Cloud provider: aws, azure, gcp, kubernetes (required)")
	cmd.Flags().StringVar(&role, "role", "", "Role or permission set to request (required)")
	cmd.Flags().StringVar(&scope, "scope", "", "Resource scope: AWS account ID, GCP project, K8s namespace (required)")
	cmd.Flags().StringVar(&duration, "duration", "", "Elevation duration, e.g. 1h, 30m (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "Justification for the request (required)")
	cmd.Flags().BoolVar(&breakGlass, "break-glass", false, "Emergency break-glass mode: bypass approval with immediate alerts")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until the request is approved or denied")

	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("role")
	_ = cmd.MarkFlagRequired("scope")
	_ = cmd.MarkFlagRequired("duration")
	_ = cmd.MarkFlagRequired("reason")

	return cmd
}
