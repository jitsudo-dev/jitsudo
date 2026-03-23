// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
