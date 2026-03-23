// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package client provides a Go client library for the jitsudo API.
// It can be used by external tools, approval bots, and automation scripts
// to interact with the jitsudod control plane.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
)

// Config holds the client configuration.
type Config struct {
	// ServerURL is the gRPC server address, e.g. "localhost:8443".
	ServerURL string
	// Token is the Bearer token to attach to every RPC.
	Token string
	// Insecure disables TLS. Use for local development only.
	Insecure bool
	// TLS configures certificate-based transport security.
	// Ignored when Insecure is true.
	TLS *TLSConfig
}

// TLSConfig holds client-side TLS credential paths.
type TLSConfig struct {
	// CAFile is the path to the CA certificate used to verify the server.
	// If empty the system certificate pool is used.
	CAFile string
	// CertFile and KeyFile enable mTLS (client certificate authentication).
	CertFile string
	KeyFile  string
	// InsecureSkipVerify skips server certificate validation. Dev only.
	InsecureSkipVerify bool
}

// Client is an authenticated gRPC client for the jitsudo API.
type Client struct {
	conn *grpc.ClientConn
	svc  jitsudov1alpha1.JitsudoServiceClient
}

// New dials the jitsudod gRPC server and returns an authenticated client.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("client: ServerURL is required")
	}

	dialOpts := []grpc.DialOption{
		grpc.WithUnaryInterceptor(bearerInterceptor(cfg.Token)),
	}

	switch {
	case cfg.Insecure:
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	case cfg.TLS != nil:
		creds, err := buildClientTLS(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("client: TLS config: %w", err)
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	default:
		// No explicit TLS config: use system pool (standard production behaviour).
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}

	conn, err := grpc.NewClient(cfg.ServerURL, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("client: dial %q: %w", cfg.ServerURL, err)
	}

	return &Client{
		conn: conn,
		svc:  jitsudov1alpha1.NewJitsudoServiceClient(conn),
	}, nil
}

// Service returns the underlying generated gRPC client for direct method calls.
func (c *Client) Service() jitsudov1alpha1.JitsudoServiceClient {
	return c.svc
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// bearerInterceptor injects an Authorization: Bearer <token> header into every RPC.
func bearerInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// buildClientTLS constructs gRPC transport credentials from a TLSConfig.
func buildClientTLS(cfg *TLSConfig) (credentials.TransportCredentials, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // intentional for dev use
	}

	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %q: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA file %q: no valid certificates found", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsCfg), nil
}
