// jitsudod is the jitsudo control plane daemon.
// It exposes a REST API (via grpc-gateway) and a native gRPC API.
//
// License: Elastic License 2.0 (ELv2)
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/jitsudo-dev/jitsudo/internal/server"
	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/jitsudo-dev/jitsudo/internal/version"
)

func main() {
	// Configure structured JSON logging.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.With().Caller().Logger()

	log.Info().
		Str("version", version.Version).
		Msg("jitsudod starting")

	dbURL := envOrDefault("JITSUDOD_DATABASE_URL", "postgres://jitsudo:jitsudo@localhost:5432/jitsudo?sslmode=disable")

	// Run database migrations before opening the pool (safe to run on every start).
	if err := store.RunMigrations(dbURL); err != nil {
		log.Fatal().Err(err).Msg("database migration failed")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database pool")
	}
	defer st.Close()

	cfg := server.Config{
		HTTPAddr:     envOrDefault("JITSUDOD_HTTP_ADDR", ":8080"),
		GRPCAddr:     envOrDefault("JITSUDOD_GRPC_ADDR", ":8443"),
		DatabaseURL:  dbURL,
		OIDCIssuer:   envOrDefault("JITSUDOD_OIDC_ISSUER", "http://localhost:5556/dex"),
		OIDCClientID: envOrDefault("JITSUDOD_OIDC_CLIENT_ID", "jitsudo-cli"),
	}

	srv := server.New(cfg, st)

	if err := srv.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("jitsudod exited with error")
	}

	log.Info().Msg("jitsudod stopped")
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
