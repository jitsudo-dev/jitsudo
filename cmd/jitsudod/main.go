// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// jitsudod is the jitsudo control plane daemon.
// It exposes a REST API (via grpc-gateway) and a native gRPC API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:           "jitsudod",
		Short:         "jitsudo control plane daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), configPath)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", os.Getenv("JITSUDOD_CONFIG"), "Path to YAML config file (overrides env vars when set)")

	cmd.AddCommand(newInitCmd())

	return cmd
}
