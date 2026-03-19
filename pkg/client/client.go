// Package client provides a Go client library for the jitsudo API.
// It can be used by external tools, approval bots, and automation scripts
// to interact with the jitsudod control plane.
//
// License: Apache 2.0
package client

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
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
	if cfg.Insecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
