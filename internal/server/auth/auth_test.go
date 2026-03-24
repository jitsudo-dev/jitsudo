// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
)

// startFakeOIDC starts an httptest.Server acting as a minimal OIDC provider.
// The discovery document advertises issuerURL as the issuer. The JWKS endpoint
// returns an empty key set (sufficient for NewVerifier, which only fetches keys
// at startup and does not verify tokens here).
func startFakeOIDC(t *testing.T, issuerURL string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   issuerURL,
			"jwks_uri": srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestNewVerifier_NormalPath verifies that NewVerifier succeeds when Issuer
// and the discovery endpoint are the same URL (the common production case).
func TestNewVerifier_NormalPath(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// issuer in the discovery doc matches the URL we connect to.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   srv.URL,
			"jwks_uri": srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer:   srv.URL,
		ClientID: "test-client",
	})
	if err != nil {
		t.Fatalf("NewVerifier (no DiscoveryURL): %v", err)
	}
}

// TestNewVerifier_IssueMismatch_Fails verifies that NewVerifier fails when
// the discovery document's issuer does not match the connection URL and no
// DiscoveryURL override is provided. This is the standard go-oidc safety check.
func TestNewVerifier_IssueMismatch_Fails(t *testing.T) {
	srv := startFakeOIDC(t, "http://other.example.com") // issuer ≠ srv.URL

	_, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer:   srv.URL,
		ClientID: "test-client",
	})
	if err == nil {
		t.Fatal("expected error for issuer mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "did not match") {
		t.Errorf("error %q does not mention issuer mismatch", err)
	}
}

// TestNewVerifier_WithDiscoveryURL verifies the Docker-internal-endpoint use case:
// the discovery document is fetched from DiscoveryURL (e.g. "http://dex:5556/dex")
// but the issuer in tokens is expected to be Issuer (e.g. "http://localhost:5556/dex").
// The server returns the public Issuer URL in its discovery doc.
func TestNewVerifier_WithDiscoveryURL(t *testing.T) {
	const publicIssuer = "http://localhost:5556/dex"

	srv := startFakeOIDC(t, publicIssuer) // discovery URL is srv.URL, but issuer = publicIssuer

	_, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer:       publicIssuer,
		DiscoveryURL: srv.URL, // connect here for discovery...
		ClientID:     "jitsudo-cli",
	})
	if err != nil {
		t.Fatalf("NewVerifier with DiscoveryURL: %v", err)
	}
}

// TestNewVerifier_DiscoveryURLUnreachable verifies that a connection error on
// DiscoveryURL (not Issuer) surfaces correctly when the override is used.
func TestNewVerifier_DiscoveryURLUnreachable(t *testing.T) {
	_, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer:       "http://localhost:5556/dex",
		DiscoveryURL: "http://127.0.0.1:19999", // nothing listening here
		ClientID:     "jitsudo-cli",
	})
	if err == nil {
		t.Fatal("expected error for unreachable DiscoveryURL, got nil")
	}
}
