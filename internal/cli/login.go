package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with the configured IdP via OIDC device flow",
		Long: `Authenticate with your identity provider using the OIDC Device Authorization Flow (RFC 8628).

This flow works without a browser redirect, making it suitable for headless terminals and SSH sessions.
Upon success, credentials are stored at ~/.jitsudo/credentials.`,
		Example: `  jitsudo login
  jitsudo login --provider https://your-idp.okta.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: implement OIDC device flow
			fmt.Fprintln(cmd.OutOrStdout(), "login: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "IdP OIDC issuer URL (overrides config)")

	return cmd
}
