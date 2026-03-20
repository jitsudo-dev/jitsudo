// Package auth handles OIDC token validation and identity extraction for the
// jitsudod control plane. It validates Bearer tokens against the configured
// IdP's JWKS endpoint and extracts group membership from token claims.
//
// License: Elastic License 2.0 (ELv2)
package auth

import (
	"context"
	"fmt"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Config holds OIDC verifier configuration.
type Config struct {
	Issuer   string // OIDC issuer URL, e.g. "http://localhost:5556/dex"
	ClientID string // Expected audience claim, e.g. "jitsudo-cli"
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
func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: OIDC discovery for %q: %w", cfg.Issuer, err)
	}
	v := provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID})
	return &Verifier{verifier: v}, nil
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
