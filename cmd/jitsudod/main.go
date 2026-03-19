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
	"github.com/jitsudo-dev/jitsudo/internal/version"
)

func main() {
	// Configure structured JSON logging.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.With().Caller().Logger()

	log.Info().
		Str("version", version.Version).
		Msg("jitsudod starting")

	cfg := server.Config{
		HTTPAddr: envOrDefault("JITSUDOD_HTTP_ADDR", ":8080"),
		GRPCAddr: envOrDefault("JITSUDOD_GRPC_ADDR", ":8443"),
	}

	srv := server.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
