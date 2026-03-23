//go:build integration

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package testutil

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/server"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// MustRunMigrations applies database migrations. Fatals on error.
func MustRunMigrations(t testing.TB, dsn string) {
	t.Helper()
	if err := store.RunMigrations(dsn); err != nil {
		t.Fatalf("MustRunMigrations: %v", err)
	}
}

// MustStartServer starts a jitsudod server in-process on two random free ports
// and returns (grpcAddr, httpAddr). The server is stopped via t.Cleanup.
// It polls /healthz until ready (10 second timeout).
func MustStartServer(t testing.TB, dbURL, oidcIssuer string) (grpcAddr, httpAddr string) {
	t.Helper()
	grpcAddr = GetFreeAddr(t)
	httpAddr = GetFreeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())

	s, err := store.New(ctx, dbURL)
	if err != nil {
		cancel()
		t.Fatalf("MustStartServer: store.New: %v", err)
	}

	cfg := server.Config{
		GRPCAddr:     grpcAddr,
		HTTPAddr:     httpAddr,
		DatabaseURL:  dbURL,
		OIDCIssuer:   oidcIssuer,
		OIDCClientID: "jitsudo-cli",
	}
	srv := server.New(cfg, s)

	errC := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			errC <- err
		}
	}()

	// Poll /healthz until the server is ready.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, httpErr := http.Get("http://" + httpAddr + "/healthz")
		if httpErr == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			t.Cleanup(func() {
				cancel()
				s.Close()
			})
			return grpcAddr, httpAddr
		}
		if resp != nil {
			resp.Body.Close()
		}
		// Surface startup errors immediately.
		select {
		case startErr := <-errC:
			cancel()
			t.Fatalf("MustStartServer: server failed to start: %v", startErr)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancel()
	t.Fatalf("MustStartServer: server at %s did not become ready within 10s", httpAddr)
	return "", "" // unreachable
}
