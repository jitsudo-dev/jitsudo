// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Internal tests for unexported transport helpers.
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostRewriteTransport_RewritesMatchingHost(t *testing.T) {
	// Target server: responds 200 when reached.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	// targetHost is e.g. "127.0.0.1:PORT"
	targetHost := target.Listener.Addr().String()

	rt := &hostRewriteTransport{
		from:    "unreachable.internal:5556",
		to:      targetHost,
		wrapped: http.DefaultTransport,
	}

	req, _ := http.NewRequest(http.MethodGet, "http://unreachable.internal:5556/dex/keys", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHostRewriteTransport_PassesThroughNonMatchingHost(t *testing.T) {
	// Server to be reached directly (non-matching host).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	rt := &hostRewriteTransport{
		from:    "other.host:9999",
		to:      "should-not-be-used:0",
		wrapped: http.DefaultTransport,
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/path", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHostOf(t *testing.T) {
	cases := []struct {
		rawURL string
		want   string
	}{
		{"http://localhost:5556/dex", "localhost:5556"},
		{"http://dex:5556/dex", "dex:5556"},
		{"https://example.com/path", "example.com"},
		{"not-a-url", ""},
	}
	for _, tc := range cases {
		got := hostOf(tc.rawURL)
		if got != tc.want {
			t.Errorf("hostOf(%q) = %q, want %q", tc.rawURL, got, tc.want)
		}
	}
}
