// License: Apache 2.0
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/emptypb"
	"gopkg.in/yaml.v3"

	"github.com/jitsudo-dev/jitsudo/internal/config"
	"github.com/jitsudo-dev/jitsudo/internal/store"
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
		newServerReloadPoliciesCmd(),
	)

	return cmd
}

// ── server init ───────────────────────────────────────────────────────────────

func newServerInitCmd() *cobra.Command {
	var (
		dbURL          string
		oidcIssuer     string
		clientID       string
		httpAddr       string
		grpcAddr       string
		configOut      string
		skipMigrations bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a new control plane instance",
		Long: `Initialize a new jitsudod control plane. Tests database connectivity,
runs migrations, and writes a starter configuration file.`,
		Example: `  jitsudo server init \
    --db-url postgres://jitsudo:password@localhost:5432/jitsudo \
    --oidc-issuer https://your-idp.okta.com \
    --oidc-client-id jitsudo-server`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbURL == "" {
				return fmt.Errorf("--db-url is required")
			}
			if oidcIssuer == "" {
				return fmt.Errorf("--oidc-issuer is required")
			}
			if clientID == "" {
				return fmt.Errorf("--oidc-client-id is required")
			}

			out := cmd.OutOrStdout()

			// 1. Test database connectivity.
			fmt.Fprintf(out, "Connecting to database... ")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			st, err := store.New(ctx, dbURL)
			if err != nil {
				fmt.Fprintln(out, "FAILED")
				return fmt.Errorf("database connectivity check failed: %w", err)
			}
			st.Close()
			fmt.Fprintln(out, "OK")

			// 2. Run migrations.
			if !skipMigrations {
				fmt.Fprintf(out, "Running database migrations... ")
				if err := store.RunMigrations(dbURL); err != nil {
					fmt.Fprintln(out, "FAILED")
					return fmt.Errorf("migration failed: %w", err)
				}
				fmt.Fprintln(out, "OK")
			}

			// 3. Write starter config file.
			cfg := &config.FileConfig{
				Server: config.ServerCfg{
					HTTPAddr: httpAddr,
					GRPCAddr: grpcAddr,
				},
				Database: config.DatabaseCfg{
					URL: dbURL,
				},
				Auth: config.AuthCfg{
					OIDCIssuer: oidcIssuer,
					ClientID:   clientID,
				},
				Log: config.LogCfg{
					Level:  "info",
					Format: "json",
				},
			}

			b, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}

			if err := os.WriteFile(configOut, b, 0600); err != nil {
				// Fall back to current directory if the target is not writable.
				fallback := "jitsudo.yaml"
				fmt.Fprintf(out, "Warning: could not write to %q (%v), writing to %q instead\n", configOut, err, fallback)
				configOut = fallback
				if err2 := os.WriteFile(configOut, b, 0600); err2 != nil {
					return fmt.Errorf("write config: %w", err2)
				}
			}

			fmt.Fprintf(out, "\nConfiguration written to: %s\n", configOut)
			fmt.Fprintf(out, "\nNext steps:\n")
			fmt.Fprintf(out, "  1. Edit %s to enable providers and notifications\n", configOut)
			fmt.Fprintf(out, "  2. Start the server: jitsudod --config %s\n", configOut)
			fmt.Fprintf(out, "  3. Log in from the CLI: jitsudo login --server localhost%s\n", httpAddr)
			return nil
		},
	}

	cmd.Flags().StringVar(&dbURL, "db-url", "", "PostgreSQL connection URL (required)")
	cmd.Flags().StringVar(&oidcIssuer, "oidc-issuer", "", "OIDC issuer URL (required)")
	cmd.Flags().StringVar(&clientID, "oidc-client-id", "", "OIDC client ID (required)")
	cmd.Flags().StringVar(&httpAddr, "http-addr", ":8080", "HTTP listen address")
	cmd.Flags().StringVar(&grpcAddr, "grpc-addr", ":8443", "gRPC listen address")
	cmd.Flags().StringVar(&configOut, "config-out", "jitsudo.yaml", "Path to write the generated configuration file")
	cmd.Flags().BoolVar(&skipMigrations, "skip-migrations", false, "Skip database migrations (use if already migrated)")

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
