package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newShellCmd() *cobra.Command {
	var shell string

	cmd := &cobra.Command{
		Use:   "shell <request-id>",
		Short: "Open an interactive shell with elevated credentials injected",
		Long: `Drop into an interactive shell with elevated credentials injected into the
subprocess environment. The parent shell never receives the credentials.

Upon exit or credential expiry, the elevated credentials are invalidated.`,
		Example: `  jitsudo shell req_01J8KZ...
  jitsudo shell req_01J8KZ... --shell zsh`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: fetch credentials, spawn interactive shell subprocess with injected env
			fmt.Fprintln(cmd.OutOrStdout(), "shell: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&shell, "shell", "", "Shell to use: bash, zsh, sh (default: $SHELL)")

	return cmd
}
