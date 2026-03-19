// Package server implements the jitsudod control plane.
// It exposes both a REST API (via grpc-gateway) and a native gRPC API.
//
// License: Elastic License 2.0 (ELv2)
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/rs/zerolog/log"
)

// Config holds the server configuration.
type Config struct {
	HTTPAddr string // e.g., ":8080"
	GRPCAddr string // e.g., ":8443"
}

// Server is the jitsudod control plane.
type Server struct {
	cfg        Config
	httpServer *http.Server
	grpcLis    net.Listener
}

// New creates a new Server with the given configuration.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Start starts the HTTP and gRPC servers. It blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerHealthHandlers(mux)

	s.httpServer = &http.Server{
		Addr:    s.cfg.HTTPAddr,
		Handler: mux,
	}

	httpErrC := make(chan error, 1)
	go func() {
		log.Info().Str("addr", s.cfg.HTTPAddr).Msg("jitsudod HTTP server starting")
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			httpErrC <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("jitsudod shutting down")
		return s.httpServer.Shutdown(context.Background())
	case err := <-httpErrC:
		return err
	}
}

// registerHealthHandlers registers /healthz and /readyz endpoints.
func (s *Server) registerHealthHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// TODO: check database connectivity before returning 200
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"version":"dev","api_versions":["v1alpha1"]}`)
	})
}
