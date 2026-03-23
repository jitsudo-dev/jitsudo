// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/jitsudo-dev/jitsudo/internal/config"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

func newInitCmd() *cobra.Command {
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
		Example: `  jitsudod init \
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
