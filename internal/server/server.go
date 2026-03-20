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
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/providers"
	awsprovider "github.com/jitsudo-dev/jitsudo/internal/providers/aws"
	azureprovider "github.com/jitsudo-dev/jitsudo/internal/providers/azure"
	gcpprovider "github.com/jitsudo-dev/jitsudo/internal/providers/gcp"
	k8sprovider "github.com/jitsudo-dev/jitsudo/internal/providers/kubernetes"
	"github.com/jitsudo-dev/jitsudo/internal/providers/mock"
	"github.com/jitsudo-dev/jitsudo/internal/server/api"
	"github.com/jitsudo-dev/jitsudo/internal/server/audit"
	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"github.com/jitsudo-dev/jitsudo/internal/server/policy"
	"github.com/jitsudo-dev/jitsudo/internal/server/workflow"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// Config holds the server configuration.
type Config struct {
	HTTPAddr     string // e.g., ":8080"
	GRPCAddr     string // e.g., ":8443"
	DatabaseURL  string // PostgreSQL DSN
	OIDCIssuer   string // e.g., "http://localhost:5556/dex"
	OIDCClientID string // e.g., "jitsudo-cli"

	// Notifications configures the optional notification channels.
	Notifications NotificationsConfig

	// Providers configures the real cloud/infrastructure providers.
	// Each is optional; nil means the provider is not registered.
	Providers ProvidersConfig
}

// NotificationsConfig holds optional notifier configurations.
type NotificationsConfig struct {
	Slack *notifications.SlackConfig `yaml:"slack"`
	SMTP  *notifications.SMTPConfig  `yaml:"smtp"`
}

// ProvidersConfig holds optional cloud provider configurations.
// Providers are only registered when their configuration is non-nil.
type ProvidersConfig struct {
	AWS        *awsprovider.Config        `yaml:"aws"`
	GCP        *gcpprovider.Config        `yaml:"gcp"`
	Azure      *azureprovider.Config      `yaml:"azure"`
	Kubernetes *k8sprovider.Config        `yaml:"kubernetes"`
}

// Server is the jitsudod control plane.
type Server struct {
	cfg        Config
	store      *store.Store
	httpServer *http.Server
	grpcServer *grpc.Server
}

// New creates a new Server with the given configuration.
func New(cfg Config, s *store.Store) *Server {
	return &Server{cfg: cfg, store: s}
}

// Start assembles all subsystems and starts both the gRPC and HTTP servers.
// It blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	// ── Auth middleware ───────────────────────────────────────────────────────
	verifier, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   s.cfg.OIDCIssuer,
		ClientID: s.cfg.OIDCClientID,
	})
	if err != nil {
		return fmt.Errorf("server: auth verifier: %w", err)
	}

	// ── Subsystems ────────────────────────────────────────────────────────────
	auditLogger := audit.NewLogger(s.store)
	policyEngine := policy.NewEngine(s.store)
	if err := policyEngine.Reload(ctx); err != nil {
		return fmt.Errorf("server: policy engine reload: %w", err)
	}

	// ── Notification dispatcher ───────────────────────────────────────────────
	var notifiers []notifications.Notifier
	if cfg := s.cfg.Notifications.Slack; cfg != nil && cfg.WebhookURL != "" {
		notifiers = append(notifiers, notifications.NewSlackNotifier(*cfg))
	}
	if cfg := s.cfg.Notifications.SMTP; cfg != nil && cfg.Host != "" {
		notifiers = append(notifiers, notifications.NewSMTPNotifier(*cfg))
	}
	dispatcher := notifications.NewDispatcher(notifiers...)

	// ── Provider registry ─────────────────────────────────────────────────────
	registry := providers.NewRegistry()
	registry.Register(mock.New()) // always available for testing and demos

	if pcfg := s.cfg.Providers.Kubernetes; pcfg != nil {
		if kp, err := k8sprovider.New(*pcfg); err != nil {
			log.Warn().Err(err).Msg("kubernetes provider: init failed, skipping")
		} else {
			registry.Register(kp)
		}
	}
	if pcfg := s.cfg.Providers.AWS; pcfg != nil {
		if ap, err := awsprovider.New(ctx, *pcfg); err != nil {
			log.Warn().Err(err).Msg("aws provider: init failed, skipping")
		} else {
			registry.Register(ap)
		}
	}
	if pcfg := s.cfg.Providers.GCP; pcfg != nil {
		if gp, err := gcpprovider.New(ctx, *pcfg); err != nil {
			log.Warn().Err(err).Msg("gcp provider: init failed, skipping")
		} else {
			registry.Register(gp)
		}
	}
	if pcfg := s.cfg.Providers.Azure; pcfg != nil {
		if azp, err := azureprovider.New(ctx, *pcfg); err != nil {
			log.Warn().Err(err).Msg("azure provider: init failed, skipping")
		} else {
			registry.Register(azp)
		}
	}

	workflowEngine := workflow.NewEngine(s.store, auditLogger, policyEngine, registry, dispatcher)
	handler := api.NewHandler(workflowEngine, s.store, auditLogger, policyEngine)

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcLis, err := net.Listen("tcp", s.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("server: gRPC listen on %s: %w", s.cfg.GRPCAddr, err)
	}

	s.grpcServer = grpc.NewServer(
		grpc.UnaryInterceptor(verifier.GRPCUnaryInterceptor()),
	)
	jitsudov1alpha1.RegisterJitsudoServiceServer(s.grpcServer, handler)

	grpcErrC := make(chan error, 1)
	go func() {
		log.Info().Str("addr", s.cfg.GRPCAddr).Msg("jitsudod gRPC server starting")
		if err := s.grpcServer.Serve(grpcLis); err != nil {
			grpcErrC <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// ── grpc-gateway HTTP mux ─────────────────────────────────────────────────
	gwMux := runtime.NewServeMux()
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := jitsudov1alpha1.RegisterJitsudoServiceHandlerFromEndpoint(ctx, gwMux, s.cfg.GRPCAddr, dialOpts); err != nil {
		return fmt.Errorf("server: gateway registration: %w", err)
	}

	// ── HTTP mux ──────────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	s.registerHealthHandlers(mux)
	mux.Handle("/api/", gwMux)

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

	// ── Expiry sweeper ────────────────────────────────────────────────────────
	go workflowEngine.RunExpirySweeper(ctx, 30*time.Second)

	// ── Wait for shutdown ─────────────────────────────────────────────────────
	select {
	case <-ctx.Done():
		log.Info().Msg("jitsudod shutting down")
		s.grpcServer.GracefulStop()
		return s.httpServer.Shutdown(context.Background())
	case err := <-grpcErrC:
		return err
	case err := <-httpErrC:
		return err
	}
}

// registerHealthHandlers registers /healthz, /readyz, and /version endpoints.
func (s *Server) registerHealthHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.store != nil {
			if err := s.store.Ping(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintf(w, "db: %v", err)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"version":"dev","api_versions":["v1alpha1"]}`)
	})
}
