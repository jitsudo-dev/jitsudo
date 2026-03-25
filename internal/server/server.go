// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package server implements the jitsudod control plane.
// It exposes both a REST API (via grpc-gateway) and a native gRPC API.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	"github.com/jitsudo-dev/jitsudo/internal/server/mcp"
	"github.com/jitsudo-dev/jitsudo/internal/server/mcpagent"
	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"github.com/jitsudo-dev/jitsudo/internal/server/policy"
	"github.com/jitsudo-dev/jitsudo/internal/server/workflow"
	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/jitsudo-dev/jitsudo/internal/version"
)

// Config holds the server configuration.
type Config struct {
	HTTPAddr         string // e.g., ":8080"
	GRPCAddr         string // e.g., ":8443"
	DatabaseURL      string // PostgreSQL DSN
	OIDCIssuer       string // e.g., "http://localhost:5556/dex"
	OIDCDiscoveryURL string // optional: Docker-internal OIDC endpoint (overrides connection target, not expected issuer)
	OIDCClientID     string // e.g., "jitsudo-cli"

	// TLS configures mTLS for the gRPC listener.
	// Leave zero-value for insecure local development.
	TLS TLSConfig

	// Notifications configures the optional notification channels.
	Notifications NotificationsConfig

	// Providers configures the real cloud/infrastructure providers.
	// Each is optional; nil means the provider is not registered.
	Providers ProvidersConfig

	// MCPToken is the Bearer token required to use the MCP approver endpoint.
	// When empty the /mcp endpoint responds with 404 (disabled).
	MCPToken string

	// MCPAgentIdentity is the name recorded in the audit log for MCP decisions.
	// Defaults to "mcp-agent" if empty.
	MCPAgentIdentity string

	// MCPAgentAddr is the listen address for the MCP agent-requestor server.
	// When non-empty a second http.Server is started on this address exposing
	// POST /mcp/agent/messages and GET /mcp/agent/sse.
	// Default ":8081"; set to "" to disable.
	MCPAgentAddr string

	// AdminEmails lists email addresses that are granted admin privileges
	// regardless of group membership. Useful when the OIDC provider does not
	// emit a groups claim (e.g. Dex static passwords in development).
	// In production, prefer group-based access via the "jitsudo-admins" group.
	AdminEmails []string
}

// TLSConfig holds paths to TLS credentials for the gRPC listener.
// CertFile + KeyFile enables server TLS. Adding CAFile enables mTLS
// (server verifies client certificates against the CA).
type TLSConfig struct {
	CertFile string
	KeyFile  string
	CAFile   string // non-empty enables mTLS client verification
}

// NotificationsConfig holds optional notifier configurations.
type NotificationsConfig struct {
	Slack    *notifications.SlackConfig     `yaml:"slack"`
	SMTP     *notifications.SMTPConfig      `yaml:"smtp"`
	Webhooks []*notifications.WebhookConfig `yaml:"webhooks"`
	SIEM     *notifications.SIEMConfig      `yaml:"siem"`
}

// ProvidersConfig holds optional cloud provider configurations.
// Providers are only registered when their configuration is non-nil.
type ProvidersConfig struct {
	AWS        *awsprovider.Config   `yaml:"aws"`
	GCP        *gcpprovider.Config   `yaml:"gcp"`
	Azure      *azureprovider.Config `yaml:"azure"`
	Kubernetes *k8sprovider.Config   `yaml:"kubernetes"`
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
		Issuer:       s.cfg.OIDCIssuer,
		DiscoveryURL: s.cfg.OIDCDiscoveryURL,
		ClientID:     s.cfg.OIDCClientID,
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

	// ── MCP agent broker (must be built before the dispatcher) ───────────────
	// The broker is appended to the notifier list so it receives every workflow
	// event. It must exist before NewDispatcher is called because the dispatcher
	// captures the notifier list at construction time.
	var agentBroker *mcpagent.Broker
	if s.cfg.MCPAgentAddr != "" {
		agentBroker = mcpagent.NewBroker(s.store)
	}

	// ── Notification dispatcher ───────────────────────────────────────────────
	var notifiers []notifications.Notifier
	if agentBroker != nil {
		notifiers = append(notifiers, agentBroker)
	}
	if cfg := s.cfg.Notifications.Slack; cfg != nil && cfg.WebhookURL != "" {
		notifiers = append(notifiers, notifications.NewSlackNotifier(*cfg))
	}
	if cfg := s.cfg.Notifications.SMTP; cfg != nil && cfg.Host != "" {
		notifiers = append(notifiers, notifications.NewSMTPNotifier(*cfg))
	}
	for _, cfg := range s.cfg.Notifications.Webhooks {
		if cfg != nil && cfg.URL != "" {
			notifiers = append(notifiers, notifications.NewWebhookNotifier(*cfg))
		}
	}
	if siem := s.cfg.Notifications.SIEM; siem != nil {
		if cfg := siem.JSON; cfg != nil && cfg.URL != "" {
			notifiers = append(notifiers, notifications.NewSIEMJSONNotifier(*cfg))
		}
		if cfg := siem.Syslog; cfg != nil {
			notifiers = append(notifiers, notifications.NewSIEMSyslogNotifier(*cfg))
		}
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
	handler := api.NewHandler(workflowEngine, s.store, auditLogger, policyEngine, s.cfg.AdminEmails)

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcLis, err := net.Listen("tcp", s.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("server: gRPC listen on %s: %w", s.cfg.GRPCAddr, err)
	}

	grpcServerOpts := []grpc.ServerOption{
		grpc.UnaryInterceptor(verifier.GRPCUnaryInterceptor()),
	}

	// Build gateway dial options. The gateway always connects to the loopback
	// gRPC address, so we configure its credentials to match the server.
	var gatewayDialOpts []grpc.DialOption

	if s.cfg.TLS.CertFile != "" && s.cfg.TLS.KeyFile != "" {
		tlsCreds, err := buildServerTLS(s.cfg.TLS)
		if err != nil {
			return fmt.Errorf("server: TLS config: %w", err)
		}
		grpcServerOpts = append(grpcServerOpts, grpc.Creds(tlsCreds))
		// Gateway → gRPC loopback: trust the server's own certificate.
		gatewayCreds, err := buildGatewayTLS(s.cfg.TLS)
		if err != nil {
			return fmt.Errorf("server: gateway TLS config: %w", err)
		}
		gatewayDialOpts = []grpc.DialOption{grpc.WithTransportCredentials(gatewayCreds)}
		log.Info().Bool("mtls", s.cfg.TLS.CAFile != "").Msg("jitsudod TLS enabled")
	} else {
		gatewayDialOpts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}

	s.grpcServer = grpc.NewServer(grpcServerOpts...)
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
	if err := jitsudov1alpha1.RegisterJitsudoServiceHandlerFromEndpoint(ctx, gwMux, s.cfg.GRPCAddr, gatewayDialOpts); err != nil {
		return fmt.Errorf("server: gateway registration: %w", err)
	}

	// ── HTTP mux ──────────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	s.registerHealthHandlers(mux)
	mux.Handle("/api/", gwMux)

	// ── MCP approver interface ────────────────────────────────────────────────
	mcpServer := mcp.New(workflowEngine, s.store, s.cfg.MCPToken, s.cfg.MCPAgentIdentity)
	mux.Handle("/mcp", mcpServer)

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

	// ── MCP agent HTTP server ─────────────────────────────────────────────────
	var mcpAgentHTTP *http.Server
	mcpAgentErrC := make(chan error, 1)
	if s.cfg.MCPAgentAddr != "" {
		agentSrv := mcpagent.New(workflowEngine, s.store, verifier, agentBroker)
		mcpAgentHTTP = &http.Server{
			Addr:    s.cfg.MCPAgentAddr,
			Handler: agentSrv.Handler(),
		}
		go func() {
			log.Info().Str("addr", s.cfg.MCPAgentAddr).Msg("jitsudod MCP agent server starting")
			if err := mcpAgentHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				mcpAgentErrC <- fmt.Errorf("MCP agent server error: %w", err)
			}
		}()
	}

	// ── Expiry sweeper ────────────────────────────────────────────────────────
	go workflowEngine.RunExpirySweeper(ctx, 30*time.Second)

	// ── Pending timeout sweeper ───────────────────────────────────────────────
	go workflowEngine.RunPendingTimeoutSweeper(ctx, 30*time.Second)

	// ── Periodic policy sync ──────────────────────────────────────────────────
	// Each instance independently re-reads policies from the database every 30s,
	// so ApplyPolicy / DeletePolicy changes propagate to all replicas without
	// requiring a fan-out ReloadPolicies RPC. No advisory lock needed — each
	// instance reloading its own cache is idempotent and has no side effects.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := policyEngine.Reload(ctx); err != nil {
					log.Warn().Err(err).Msg("policy sync: reload failed")
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// ── Wait for shutdown ─────────────────────────────────────────────────────
	select {
	case <-ctx.Done():
		log.Info().Msg("jitsudod shutting down")
		s.grpcServer.GracefulStop()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if mcpAgentHTTP != nil {
			_ = mcpAgentHTTP.Shutdown(shutdownCtx)
		}
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-grpcErrC:
		return err
	case err := <-httpErrC:
		return err
	case err := <-mcpAgentErrC:
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
		_, _ = fmt.Fprintf(w, `{"version":%q,"api_versions":["v1alpha1"]}`, version.Version)
	})
}

// buildServerTLS constructs gRPC server credentials from TLSConfig.
// CAFile non-empty enables mTLS (client certificate verification).
func buildServerTLS(cfg TLSConfig) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load key pair: %w", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA file: no valid certificates found")
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return credentials.NewTLS(tlsCfg), nil
}

// buildGatewayTLS constructs gRPC client credentials for the grpc-gateway →
// gRPC loopback connection. It trusts the server's own certificate so that
// self-signed certs work without distributing a separate CA bundle.
func buildGatewayTLS(cfg TLSConfig) (credentials.TransportCredentials, error) {
	certPEM, err := os.ReadFile(cfg.CertFile)
	if err != nil {
		return nil, fmt.Errorf("read server cert for gateway: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("parse server cert for gateway: no valid certificates found")
	}
	return credentials.NewTLS(&tls.Config{RootCAs: pool}), nil
}
