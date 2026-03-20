package cli

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
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
			ctx := cmd.Context()
			requestID := args[0]

			// Determine shell to use.
			shellBin := shell
			if shellBin == "" {
				shellBin = os.Getenv("SHELL")
			}
			if shellBin == "" {
				shellBin = "/bin/sh"
			}

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{
				RequestId: requestID,
			})
			if err != nil {
				return fmt.Errorf("get credentials: %w", err)
			}

			// Build environment: inherit parent env + inject credentials.
			env := os.Environ()
			for _, cred := range resp.GetGrant().GetCredentials() {
				env = append(env, cred.GetName()+"="+cred.GetValue())
			}
			env = append(env, "JITSUDO_ELEVATED=1")
			env = append(env, "JITSUDO_REQUEST_ID="+requestID)

			// Print warning banner to stderr to avoid polluting stdout pipelines.
			expiresAt := resp.GetGrant().GetExpiresAt().AsTime()
			var expiryNote string
			if !expiresAt.IsZero() {
				expiryNote = fmt.Sprintf("Credentials expire at %s", expiresAt.Local().Format(time.RFC3339))
			} else {
				expiryNote = "No expiry information available"
			}
			fmt.Fprintf(cmd.ErrOrStderr(),
				"\n*** jitsudo elevated shell — request %s ***\n*** %s ***\n*** Type 'exit' to leave the elevated context ***\n\n",
				requestID, expiryNote,
			)

			proc := exec.CommandContext(ctx, shellBin)
			proc.Env = env
			proc.Stdin = cmd.InOrStdin()
			proc.Stdout = cmd.OutOrStdout()
			proc.Stderr = cmd.ErrOrStderr()

			if err := proc.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				return fmt.Errorf("shell: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&shell, "shell", "", "Shell to use: bash, zsh, sh (default: $SHELL)")

	return cmd
}
