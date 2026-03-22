// jitsudod is the jitsudo control plane daemon.
// It exposes a REST API (via grpc-gateway) and a native gRPC API.
//
// License: Elastic License 2.0 (ELv2)
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/jitsudo-dev/jitsudo/internal/config"
	"github.com/jitsudo-dev/jitsudo/internal/server"
	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/jitsudo-dev/jitsudo/internal/version"
)

func main() {
	configPath := flag.String("config", os.Getenv("JITSUDOD_CONFIG"), "Path to YAML config file (overrides env vars when set)")
	flag.Parse()

	// Configure structured JSON logging.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.With().Caller().Logger()

	log.Info().
		Str("version", version.Version).
		Msg("jitsudod starting")

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	// Apply log level from config.
	if lvl, err := zerolog.ParseLevel(cfg.Log.Level); err == nil {
		zerolog.SetGlobalLevel(lvl)
	}

	// Run database migrations before opening the pool (safe to run on every start).
	if err := store.RunMigrations(cfg.Database.URL); err != nil {
		log.Fatal().Err(err).Msg("database migration failed")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, cfg.Database.URL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database pool")
	}
	defer st.Close()

	srv := server.New(server.Config{
		HTTPAddr:     cfg.Server.HTTPAddr,
		GRPCAddr:     cfg.Server.GRPCAddr,
		DatabaseURL:  cfg.Database.URL,
		OIDCIssuer:   cfg.Auth.OIDCIssuer,
		OIDCClientID: cfg.Auth.ClientID,
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
		},
		MCPToken:         cfg.MCP.Token,
		MCPAgentIdentity: cfg.MCP.AgentIdentity,
	}, st)

	if err := srv.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("jitsudod exited with error")
	}

	log.Info().Msg("jitsudod stopped")
}
