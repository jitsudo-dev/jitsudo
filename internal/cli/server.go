package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Control plane management commands",
	}

	cmd.AddCommand(
		newServerInitCmd(),
		newServerStatusCmd(),
		newServerVersionCmd(),
	)

	return cmd
}

func newServerInitCmd() *cobra.Command {
	var (
		dbURL      string
		oidcIssuer string
		clientID   string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a new control plane instance",
		Long: `Initialize a new jitsudod control plane. Runs database migrations,
generates a default configuration file, and optionally creates a systemd service unit.`,
		Example: `  jitsudo server init \
    --db-url postgres://jitsudo:password@localhost:5432/jitsudo \
    --oidc-issuer https://your-idp.okta.com \
    --oidc-client-id jitsudo-server`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "server init: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&dbURL, "db-url", "", "PostgreSQL connection URL (required)")
	cmd.Flags().StringVar(&oidcIssuer, "oidc-issuer", "", "OIDC issuer URL (required)")
	cmd.Flags().StringVar(&clientID, "oidc-client-id", "", "OIDC client ID (required)")

	return cmd
}

func newServerStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check control plane health",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "server status: not yet implemented")
			return nil
		},
	}
}

func newServerVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print server version and API compatibility",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "server version: not yet implemented")
			return nil
		},
	}
}
