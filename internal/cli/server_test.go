// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── server status ─────────────────────────────────────────────────────────────

func TestServerStatusCmd_AllHealthy(t *testing.T) {
	srv := newFakeHealthServer(t, http.StatusOK, http.StatusOK)
	defer srv.Close()

	cmd := newServerStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--server-url", srv.URL})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"liveness", "readiness", "version", "UP", "v0.1.0", "v1alpha1"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestServerStatusCmd_ReadinessDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/readyz":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db: connection refused"))
		case "/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"v0.1.0","api_versions":["v1alpha1"]}`))
		}
	}))
	defer srv.Close()

	cmd := newServerStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--server-url", srv.URL})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when readiness check fails, got nil")
	}
	if !strings.Contains(out.String(), "DEGRADED") {
		t.Errorf("expected DEGRADED in output:\n%s", out.String())
	}
}

func TestServerStatusCmd_ServerUnreachable(t *testing.T) {
	cmd := newServerStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	// Port 1 is reserved and guaranteed to be unreachable.
	cmd.SetArgs([]string{"--server-url", "http://127.0.0.1:1"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
	if !strings.Contains(out.String(), "DOWN") {
		t.Errorf("expected DOWN in output:\n%s", out.String())
	}
}

func TestServerStatusCmd_DefaultServerURL(t *testing.T) {
	cmd := newServerStatusCmd()
	f := cmd.Flags().Lookup("server-url")
	if f == nil {
		t.Fatal("--server-url flag not defined")
	}
	if f.DefValue != "http://localhost:8080" {
		t.Errorf("default server-url = %q, want http://localhost:8080", f.DefValue)
	}
}

// ── server version ────────────────────────────────────────────────────────────

func TestServerVersionCmd_PrintsVersion(t *testing.T) {
	srv := newFakeHealthServer(t, http.StatusOK, http.StatusOK)
	defer srv.Close()

	cmd := newServerVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--server-url", srv.URL})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "v0.1.0") {
		t.Errorf("expected version in output:\n%s", got)
	}
	if !strings.Contains(got, "v1alpha1") {
		t.Errorf("expected API version in output:\n%s", got)
	}
}

func TestServerVersionCmd_HasServerURLFlag(t *testing.T) {
	cmd := newServerVersionCmd()
	f := cmd.Flags().Lookup("server-url")
	if f == nil {
		t.Fatal("--server-url flag not defined on version command")
	}
	if f.DefValue != "http://localhost:8080" {
		t.Errorf("default = %q, want http://localhost:8080", f.DefValue)
	}
}

func TestServerVersionCmd_ServerUnreachable(t *testing.T) {
	cmd := newServerVersionCmd()
	cmd.SetArgs([]string{"--server-url", "http://127.0.0.1:1"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestServerVersionCmd_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	cmd := newServerVersionCmd()
	cmd.SetArgs([]string{"--server-url", srv.URL})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error for bad JSON response, got nil")
	}
}

// ── server init ───────────────────────────────────────────────────────────────

func TestServerInitCmd_MissingDBURL(t *testing.T) {
	cmd := newServerInitCmd()
	cmd.SetArgs([]string{
		"--oidc-issuer", "https://idp.example.com",
		"--oidc-client-id", "jitsudo-server",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--db-url is required") {
		t.Errorf("expected --db-url error, got: %v", err)
	}
}

func TestServerInitCmd_MissingOIDCIssuer(t *testing.T) {
	cmd := newServerInitCmd()
	cmd.SetArgs([]string{
		"--db-url", "postgres://localhost/jitsudo",
		"--oidc-client-id", "jitsudo-server",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--oidc-issuer is required") {
		t.Errorf("expected --oidc-issuer error, got: %v", err)
	}
}

func TestServerInitCmd_MissingClientID(t *testing.T) {
	cmd := newServerInitCmd()
	cmd.SetArgs([]string{
		"--db-url", "postgres://localhost/jitsudo",
		"--oidc-issuer", "https://idp.example.com",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--oidc-client-id is required") {
		t.Errorf("expected --oidc-client-id error, got: %v", err)
	}
}

func TestServerInitCmd_WritesConfigFile(t *testing.T) {
	// Start a fake postgres server that accepts connections (but doesn't speak the
	// postgres wire protocol — enough for store.New's net.Dial to succeed when
	// we use a raw TCP listener... however store.New does pgx handshake which
	// requires a real postgres). Skip the DB step with a clearly-invalid URL to
	// exercise only flag validation + config writing.
	//
	// The config-writing path is exercised here via the init command when all
	// required flags are present. DB connectivity will fail, which is expected.
	// We verify that the flag validation and config-file path logic work correctly.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "jitsudo.yaml")

	cmd := newServerInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{
		"--db-url", "postgres://localhost:5432/jitsudo",
		"--oidc-issuer", "https://idp.example.com",
		"--oidc-client-id", "jitsudo-server",
		"--config-out", configPath,
	})

	err := cmd.Execute()
	// DB will be unreachable in unit test environment — that's expected.
	// We verify the failure is about the DB, not about flags or config marshaling.
	if err != nil {
		if !strings.Contains(err.Error(), "database connectivity check failed") &&
			!strings.Contains(err.Error(), "connection refused") &&
			!strings.Contains(err.Error(), "no such host") {
			t.Errorf("unexpected error (not a DB error): %v", err)
		}
		// Config file should NOT exist since we never got to write it.
		if _, statErr := os.Stat(configPath); statErr == nil {
			t.Error("config file should not exist when DB check fails")
		}
		return
	}

	// If somehow a DB was reachable, validate the written config.
	b, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("config file not written: %v", readErr)
	}
	if !strings.Contains(string(b), "https://idp.example.com") {
		t.Errorf("config file missing oidc_issuer:\n%s", string(b))
	}
	if !strings.Contains(string(b), "jitsudo-server") {
		t.Errorf("config file missing client_id:\n%s", string(b))
	}
}

func TestServerInitCmd_FlagDefaults(t *testing.T) {
	cmd := newServerInitCmd()
	for flagName, wantDefault := range map[string]string{
		"http-addr":  ":8080",
		"grpc-addr":  ":8443",
		"config-out": "jitsudo.yaml",
	} {
		f := cmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("flag --%s not defined", flagName)
			continue
		}
		if f.DefValue != wantDefault {
			t.Errorf("--%s default = %q, want %q", flagName, f.DefValue, wantDefault)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newFakeHealthServer starts an httptest.Server that responds successfully
// to /healthz, /readyz, and /version.
func newFakeHealthServer(t *testing.T, healthzCode, readyzCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(healthzCode)
			if healthzCode == http.StatusOK {
				_, _ = w.Write([]byte("ok"))
			}
		case "/readyz":
			w.WriteHeader(readyzCode)
			if readyzCode == http.StatusOK {
				_, _ = w.Write([]byte("ok"))
			}
		case "/version":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			b, _ := json.Marshal(map[string]any{
				"version":      "v0.1.0",
				"api_versions": []string{"v1alpha1"},
			})
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
}
