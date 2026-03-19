package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Manage eligibility and approval policies (admin role required)",
	}

	cmd.AddCommand(
		newPolicyListCmd(),
		newPolicyGetCmd(),
		newPolicyApplyCmd(),
		newPolicyDeleteCmd(),
		newPolicyEvalCmd(),
	)

	return cmd
}

func newPolicyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all policies",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "policy list: not yet implemented")
			return nil
		},
	}
}

func newPolicyGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <policy-id>",
		Short: "Get a policy by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "policy get: not yet implemented")
			return nil
		},
	}
}

func newPolicyApplyCmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:     "apply",
		Short:   "Create or update a policy from a Rego file",
		Example: `  jitsudo policy apply -f eligibility.rego`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "policy apply: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the Rego policy file (required)")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func newPolicyDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <policy-id>",
		Short: "Delete a policy by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "policy delete: not yet implemented")
			return nil
		},
	}
}

func newPolicyEvalCmd() *cobra.Command {
	var input string

	cmd := &cobra.Command{
		Use:     "eval",
		Short:   "Dry-run policy evaluation against the current policy set",
		Example: `  jitsudo policy eval --input '{"role":"prod-infra-admin","provider":"aws","user":{"groups":["sre-oncall"]}}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "policy eval: not yet implemented")
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "", "JSON-encoded OPA input document (required)")
	_ = cmd.MarkFlagRequired("input")

	return cmd
}
