// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/emptypb"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Control plane management commands",
	}

	cmd.AddCommand(
		newServerStatusCmd(),
		newServerVersionCmd(),
		newServerReloadPoliciesCmd(),
	)

	return cmd
}

// ── server status ─────────────────────────────────────────────────────────────

func newServerStatusCmd() *cobra.Command {
	var serverURL string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check control plane health",
		Long:  "Polls the jitsudod health endpoints and prints a status summary.",
		Example: `  jitsudo server status
  jitsudo server status --server-url http://jitsudo.internal:8080`,
		RunE: func(cmd *cobra.Command, args []string) error {
			httpClient := &http.Client{Timeout: 5 * time.Second}
			out := cmd.OutOrStdout()

			type check struct {
				name   string
				url    string
				detail func(body []byte) string
			}

			checks := []check{
				{
					name:   "liveness",
					url:    serverURL + "/healthz",
					detail: func(_ []byte) string { return "jitsudod is running" },
				},
				{
					name: "readiness",
					url:  serverURL + "/readyz",
					detail: func(body []byte) string {
						if len(body) > 0 && string(body) != "ok" {
							return string(body)
						}
						return "database connection ok"
					},
				},
				{
					name: "version",
					url:  serverURL + "/version",
					detail: func(body []byte) string {
						var v struct {
							Version     string   `json:"version"`
							APIVersions []string `json:"api_versions"`
						}
						if err := json.Unmarshal(body, &v); err != nil {
							return string(body)
						}
						api := ""
						if len(v.APIVersions) > 0 {
							api = fmt.Sprintf(" (API: %s)", v.APIVersions[0])
						}
						return fmt.Sprintf("%s%s", v.Version, api)
					},
				},
			}

			tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "Component\tStatus\tDetail")
			fmt.Fprintln(tw, "---------\t------\t------")

			allOK := true
			for _, c := range checks {
				resp, err := httpClient.Get(c.url)
				if err != nil {
					fmt.Fprintf(tw, "%s\tDOWN\tcannot reach %s: %v\n", c.name, serverURL, err)
					allOK = false
					continue
				}
				body := make([]byte, 4096)
				n, _ := resp.Body.Read(body)
				resp.Body.Close()
				body = body[:n]

				if resp.StatusCode == http.StatusOK {
					fmt.Fprintf(tw, "%s\tUP\t%s\n", c.name, c.detail(body))
				} else {
					fmt.Fprintf(tw, "%s\tDEGRADED\t%s\n", c.name, string(body))
					allOK = false
				}
			}
			tw.Flush()

			if !allOK {
				return fmt.Errorf("one or more health checks failed")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&serverURL, "server-url", "http://localhost:8080", "jitsudod HTTP base URL")
	return cmd
}

// ── server version ────────────────────────────────────────────────────────────

func newServerVersionCmd() *cobra.Command {
	var serverURL string

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print server version and API compatibility",
		RunE: func(cmd *cobra.Command, args []string) error {
			httpClient := &http.Client{Timeout: 5 * time.Second}
			resp, err := httpClient.Get(serverURL + "/version")
			if err != nil {
				return fmt.Errorf("cannot reach server at %s: %w", serverURL, err)
			}
			defer resp.Body.Close()
			var v struct {
				Version     string   `json:"version"`
				APIVersions []string `json:"api_versions"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
				return fmt.Errorf("parse version response: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Server version: %s\n", v.Version)
			for _, api := range v.APIVersions {
				fmt.Fprintf(out, "API version:    %s\n", api)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&serverURL, "server-url", "http://localhost:8080", "jitsudod HTTP base URL")
	return cmd
}

// ── server reload-policies ────────────────────────────────────────────────────

func newServerReloadPoliciesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload-policies",
		Short: "Trigger the OPA policy engine to reload from the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := newClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.Service().ReloadPolicies(ctx, &emptypb.Empty{})
			if err != nil {
				return fmt.Errorf("reload-policies: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Policy engine reloaded. Active policies: %d\n", resp.GetPoliciesLoaded())
			return nil
		},
	}
}
