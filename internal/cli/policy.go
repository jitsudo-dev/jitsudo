// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
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
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().ListPolicies(ctx, &jitsudov1alpha1.ListPoliciesInput{})
			if err != nil {
				return fmt.Errorf("list policies: %w", friendlyError(err))
			}

			policies := resp.GetPolicies()
			if len(policies) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No policies found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tTYPE\tENABLED\tUPDATED")
			fmt.Fprintln(w, strings.Repeat("-", 80))
			for _, p := range policies {
				updated := p.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339)
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\n",
					p.GetId(), p.GetName(), policyTypeString(p.GetType()), p.GetEnabled(), updated)
			}
			return w.Flush()
		},
	}
}

func newPolicyGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <policy-id>",
		Short: "Get a policy by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().GetPolicy(ctx, &jitsudov1alpha1.GetPolicyInput{Id: args[0]})
			if err != nil {
				return fmt.Errorf("get policy: %w", friendlyError(err))
			}

			p := resp.GetPolicy()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ID:          %s\n", p.GetId())
			fmt.Fprintf(out, "Name:        %s\n", p.GetName())
			fmt.Fprintf(out, "Type:        %s\n", policyTypeString(p.GetType()))
			fmt.Fprintf(out, "Enabled:     %v\n", p.GetEnabled())
			fmt.Fprintf(out, "Description: %s\n", p.GetDescription())
			fmt.Fprintf(out, "Updated:     %s\n", p.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339))
			fmt.Fprintf(out, "\n--- Rego ---\n%s\n", p.GetRego())
			return nil
		},
	}
}

func newPolicyApplyCmd() *cobra.Command {
	var (
		file        string
		name        string
		policyType  string
		description string
		disable     bool
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Create or update a policy from a Rego file",
		Example: `  jitsudo policy apply -f eligibility.rego --name my-policy
  jitsudo policy apply -f approval.rego --name my-approval --type approval`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			regoBytes, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read policy file: %w", err)
			}

			// Derive name from filename if not provided.
			if name == "" {
				base := file
				if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
					base = base[idx+1:]
				}
				name = strings.TrimSuffix(base, ".rego")
			}

			var pt jitsudov1alpha1.PolicyType
			switch strings.ToLower(policyType) {
			case "approval":
				pt = jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL
			default:
				pt = jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY
			}

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
				Name:        name,
				Type:        pt,
				Rego:        string(regoBytes),
				Description: description,
				Enabled:     !disable,
			})
			if err != nil {
				return fmt.Errorf("apply policy: %w", friendlyError(err))
			}

			p := resp.GetPolicy()
			fmt.Fprintf(cmd.OutOrStdout(), "Policy %s (%s) applied — id: %s\n",
				p.GetName(), policyTypeString(p.GetType()), p.GetId())
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to the Rego policy file (required)")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().StringVar(&name, "name", "", "Policy name (default: filename without .rego extension)")
	cmd.Flags().StringVar(&policyType, "type", "eligibility", "Policy type: eligibility or approval")
	cmd.Flags().StringVar(&description, "description", "", "Human-readable description")
	cmd.Flags().BoolVar(&disable, "disable", false, "Create the policy in disabled state")

	return cmd
}

func newPolicyDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <policy-id>",
		Short: "Delete a policy by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			_, err = c.Service().DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: args[0]})
			if err != nil {
				return fmt.Errorf("delete policy: %w", friendlyError(err))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Policy %s deleted.\n", args[0])
			return nil
		},
	}
}

func newPolicyEvalCmd() *cobra.Command {
	var (
		input      string
		policyType string
	)

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Dry-run policy evaluation against the current policy set",
		Long: `Evaluates the configured policies against a JSON input document without
making any state changes. The input must match the OPA input structure:

  {"user":{"email":"...","groups":["..."]},"request":{"provider":"...","role":"...","resource_scope":"...","duration_seconds":3600}}`,
		Example: `  jitsudo policy eval --input '{"user":{"email":"alice@example.com","groups":["sre"]},"request":{"provider":"aws","role":"prod-admin","resource_scope":"123456789012","duration_seconds":3600}}'
  jitsudo policy eval --type approval --input '...'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			var pt jitsudov1alpha1.PolicyType
			switch strings.ToLower(policyType) {
			case "approval":
				pt = jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL
			default:
				pt = jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY
			}

			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().EvalPolicy(ctx, &jitsudov1alpha1.EvalPolicyInput{
				InputJson: input,
				Type:      pt,
			})
			if err != nil {
				return fmt.Errorf("eval policy: %w", friendlyError(err))
			}

			out := cmd.OutOrStdout()
			if resp.GetAllowed() {
				fmt.Fprintln(out, "allowed: true")
			} else {
				fmt.Fprintln(out, "allowed: false")
			}
			if resp.GetReason() != "" {
				fmt.Fprintf(out, "reason:  %s\n", resp.GetReason())
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "", "JSON-encoded OPA input document (required)")
	_ = cmd.MarkFlagRequired("input")
	cmd.Flags().StringVar(&policyType, "type", "eligibility", "Policy type to evaluate: eligibility or approval")

	return cmd
}

// policyTypeString converts a PolicyType proto enum to a short display string.
func policyTypeString(t jitsudov1alpha1.PolicyType) string {
	switch t {
	case jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY:
		return "eligibility"
	case jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL:
		return "approval"
	default:
		return "unknown"
	}
}
