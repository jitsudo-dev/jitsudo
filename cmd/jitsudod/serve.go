// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package main

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/jitsudo-dev/jitsudo/internal/config"
	"github.com/jitsudo-dev/jitsudo/internal/server"
	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/jitsudo-dev/jitsudo/internal/version"
)

func runServe(ctx context.Context, configPath string) error {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.With().Caller().Logger()

	log.Info().
		Str("version", version.Version).
		Msg("jitsudod starting")

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	if lvl, err := zerolog.ParseLevel(cfg.Log.Level); err == nil {
		zerolog.SetGlobalLevel(lvl)
	}

	if err := store.RunMigrations(cfg.Database.URL); err != nil {
		log.Fatal().Err(err).Msg("database migration failed")
	}

	st, err := store.New(ctx, cfg.Database.URL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database pool")
	}
	defer st.Close()

	srv := server.New(server.Config{
		HTTPAddr:         cfg.Server.HTTPAddr,
		GRPCAddr:         cfg.Server.GRPCAddr,
		DatabaseURL:      cfg.Database.URL,
		OIDCIssuer:       cfg.Auth.OIDCIssuer,
		OIDCDiscoveryURL: cfg.Auth.OIDCDiscoveryURL,
		OIDCClientID:     cfg.Auth.ClientID,
		TLS: server.TLSConfig{
			CertFile: cfg.TLS.CertFile,
			KeyFile:  cfg.TLS.KeyFile,
			CAFile:   cfg.TLS.CAFile,
		},
		Providers: server.ProvidersConfig{
			AWS:        cfg.Providers.AWS,
			GCP:        cfg.Providers.GCP,
			Azure:      cfg.Providers.Azure,
			Kubernetes: cfg.Providers.Kubernetes,
		},
		Notifications: server.NotificationsConfig{
			Slack:    cfg.Notifications.Slack,
			SMTP:     cfg.Notifications.SMTP,
			Webhooks: cfg.Notifications.Webhooks,
			SIEM:     cfg.Notifications.SIEM,
		},
		MCPToken:         cfg.MCP.Token,
		MCPAgentIdentity: cfg.MCP.AgentIdentity,
	}, st)

	if err := srv.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("jitsudod exited with error")
	}

	log.Info().Msg("jitsudod stopped")
	return nil
}
