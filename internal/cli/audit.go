package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	var (
		user     string
		provider string
		since    string
		until    string
		output   string
	)

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query the audit log",
		Example: `  jitsudo audit
  jitsudo audit --user alice@example.com
  jitsudo audit --provider aws --since 24h
  jitsudo audit --since 2026-01-01T00:00:00Z --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: query audit log from control plane and display
			fmt.Fprintln(cmd.OutOrStdout(), "audit: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&user, "user", "", "Filter by actor identity")
	cmd.Flags().StringVar(&provider, "provider", "", "Filter by provider")
	cmd.Flags().StringVar(&since, "since", "", "Return events after this duration or timestamp (e.g. 24h, 2026-01-01T00:00:00Z)")
	cmd.Flags().StringVar(&until, "until", "", "Return events before this timestamp")
	cmd.Flags().StringVar(&output, "output", "table", "Output format: table, json, csv")

	return cmd
}
