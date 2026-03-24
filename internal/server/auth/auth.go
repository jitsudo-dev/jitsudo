// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package auth handles OIDC token validation and identity extraction for the
// jitsudod control plane. It validates Bearer tokens against the configured
// IdP's JWKS endpoint and extracts group membership from token claims.
package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Config holds OIDC verifier configuration.
type Config struct {
	Issuer       string // OIDC issuer URL, e.g. "http://localhost:5556/dex"
	DiscoveryURL string // Optional: override the OIDC discovery endpoint (e.g. Docker-internal URL). Defaults to Issuer when empty.
	ClientID     string // Expected audience claim, e.g. "jitsudo-cli"
}

// Identity holds the verified claims extracted from an OIDC ID token.
type Identity struct {
	Subject string
	Email   string
	Groups  []string
}

// Verifier validates OIDC ID tokens and extracts Identity claims.
type Verifier struct {
	verifier *gooidc.IDTokenVerifier
}

// NewVerifier constructs a Verifier by fetching the OIDC discovery document.
// When cfg.DiscoveryURL is set, the discovery document is fetched from that URL
// but tokens are expected to carry cfg.Issuer as their "iss" claim. This supports
// deployments where the OIDC provider is reachable only via an internal address
// (e.g. Docker service name) while tokens use a public or host-facing issuer URL.
//
// When the two URLs have different hosts (e.g. "localhost:5556" vs "dex:5556"), a
// host-rewriting HTTP transport is injected so that JWKS fetches — which use the
// issuer URL reported inside the discovery document — are transparently redirected
// to the reachable discovery host. Without this, go-oidc would try to fetch JWKS
// from the issuer host, which may be unreachable from inside a Docker network.
//
// Security note: cfg.DiscoveryURL MUST point to the same OIDC provider as cfg.Issuer.
// Setting it to a different provider's endpoint would allow that provider's keys to be
// used for verification, which is a security vulnerability. This option exists only to
// decouple the network-level connection endpoint from the issuer URL in tokens.
func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	discoveryURL := cfg.Issuer
	if cfg.DiscoveryURL != "" {
		discoveryURL = cfg.DiscoveryURL
		ctx = gooidc.InsecureIssuerURLContext(ctx, cfg.Issuer)

		// If the issuer and discovery hosts differ, rewrite all OIDC HTTP requests
		// (including JWKS fetches) from the issuer host to the discovery host.
		// go-oidc captures the context's HTTP client at NewProvider time and reuses
		// it for all subsequent key set fetches, so this one injection is sufficient.
		issuerHost := hostOf(cfg.Issuer)
		discoveryHost := hostOf(cfg.DiscoveryURL)
		if issuerHost != "" && discoveryHost != "" && issuerHost != discoveryHost {
			ctx = gooidc.ClientContext(ctx, &http.Client{
				Transport: &hostRewriteTransport{
					from:    issuerHost,
					to:      discoveryHost,
					wrapped: http.DefaultTransport,
				},
			})
		}
	}
	provider, err := gooidc.NewProvider(ctx, discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("auth: OIDC discovery for %q: %w", discoveryURL, err)
	}
	v := provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})
	return &Verifier{verifier: v}, nil
}

// hostOf extracts the host:port from a URL string, returning "" on parse error.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// hostRewriteTransport is an http.RoundTripper that rewrites requests whose
// host matches `from` to use `to` instead. Used to redirect OIDC JWKS fetches
// from the public issuer host to an internally-reachable discovery host.
type hostRewriteTransport struct {
	from    string // host[:port] to replace, e.g. "localhost:5556"
	to      string // replacement host[:port], e.g. "dex:5556"
	wrapped http.RoundTripper
}

func (t *hostRewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == t.from {
		r2 := r.Clone(r.Context())
		r2.URL.Host = t.to
		r = r2
	}
	return t.wrapped.RoundTrip(r)
}

// Verify validates rawToken and returns the extracted Identity.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Identity, error) {
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("auth: token verification: %w", err)
	}

	var claims struct {
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("auth: claim extraction: %w", err)
	}

	return &Identity{
		Subject: token.Subject,
		Email:   claims.Email,
		Groups:  claims.Groups,
	}, nil
}

type contextKey struct{}

// GRPCUnaryInterceptor returns a gRPC unary interceptor that validates Bearer tokens
// and injects the *Identity into the request context.
func (v *Verifier) GRPCUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		vals := md.Get("authorization")
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		token := strings.TrimPrefix(vals[0], "Bearer ")
		if token == vals[0] {
			return nil, status.Error(codes.Unauthenticated, "authorization header must use Bearer scheme")
		}

		identity, err := v.Verify(ctx, token)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
		}

		return handler(context.WithValue(ctx, contextKey{}, identity), req)
	}
}

// FromContext extracts the *Identity injected by the gRPC interceptor.
// Returns nil if the context carries no identity (unauthenticated path).
func FromContext(ctx context.Context) *Identity {
	v, _ := ctx.Value(contextKey{}).(*Identity)
	return v
}

// WithIdentity returns a copy of ctx with id injected as the authenticated
// identity. Intended for use in tests and middleware that pre-validates tokens
// outside the standard gRPC interceptor path.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}
