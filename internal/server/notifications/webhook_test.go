// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── WebhookNotifier tests ─────────────────────────────────────────────────────

func TestWebhookNotifier_PostsJSON(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL})
	event := Event{
		Type:      EventApproved,
		RequestID: "req-001",
		Actor:     "approver@example.com",
		Provider:  "aws",
		Role:      "ReadOnly",
		Scope:     "123456789012",
		Reason:    "incident investigation",
	}
	if err := n.Notify(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("no request captured")
	}
	if capturedReq.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", capturedReq.Method)
	}
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var p webhookPayload
	if err := json.Unmarshal(capturedBody, &p); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if p.Type != string(EventApproved) {
		t.Errorf("type = %q, want %q", p.Type, EventApproved)
	}
	if p.RequestID != "req-001" {
		t.Errorf("request_id = %q, want req-001", p.RequestID)
	}
	if p.Actor != "approver@example.com" {
		t.Errorf("actor = %q, want approver@example.com", p.Actor)
	}
	if p.Provider != "aws" {
		t.Errorf("provider = %q, want aws", p.Provider)
	}
	if p.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestWebhookNotifier_CustomHeaders(t *testing.T) {
	var capturedAuthz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthz = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer secret-token"},
	})
	if err := n.Notify(context.Background(), Event{Type: EventApproved}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedAuthz != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuthz, "Bearer secret-token")
	}
}

func TestWebhookNotifier_HMACSignature(t *testing.T) {
	const secret = "super-secret-key"
	var capturedSig string
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = r.Header.Get("X-Jitsudo-Signature-256")
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL, Secret: secret})
	if err := n.Notify(context.Background(), Event{Type: EventBreakGlass, RequestID: "req-bg"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedSig == "" {
		t.Fatal("expected X-Jitsudo-Signature-256 header, got none")
	}
	if !strings.HasPrefix(capturedSig, "sha256=") {
		t.Errorf("signature header = %q, expected sha256= prefix", capturedSig)
	}

	// Verify the HMAC against the captured body.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(capturedBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if capturedSig != want {
		t.Errorf("signature = %q, want %q", capturedSig, want)
	}
}

func TestWebhookNotifier_NoSignatureWhenNoSecret(t *testing.T) {
	var capturedSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = r.Header.Get("X-Jitsudo-Signature-256")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL}) // no secret
	if err := n.Notify(context.Background(), Event{Type: EventApproved}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedSig != "" {
		t.Errorf("expected no signature header, got %q", capturedSig)
	}
}

func TestWebhookNotifier_EventFilter_Skips(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{
		URL:    srv.URL,
		Events: []string{"approved", "denied"}, // break_glass not in list
	})
	if err := n.Notify(context.Background(), Event{Type: EventBreakGlass}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("expected no HTTP call for filtered-out event type")
	}
}

func TestWebhookNotifier_EventFilter_Passes(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{
		URL:    srv.URL,
		Events: []string{"approved", "break_glass"},
	})
	if err := n.Notify(context.Background(), Event{Type: EventBreakGlass}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected HTTP call for event type in filter")
	}
}

func TestWebhookNotifier_EventFilter_EmptyAllowsAll(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL}) // empty Events → all allowed
	if err := n.Notify(context.Background(), Event{Type: EventExpired}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected HTTP call when event filter is empty")
	}
}

func TestWebhookNotifier_NonOKResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL})
	if err := n.Notify(context.Background(), Event{Type: EventApproved}); err == nil {
		t.Error("expected error for 5xx response")
	}
}

func TestWebhookNotifier_ExpiresAtIncludedWhenSet(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL})
	if err := n.Notify(context.Background(), Event{Type: EventApproved, ExpiresAt: expiry}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var p webhookPayload
	if err := json.Unmarshal(capturedBody, &p); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if p.ExpiresAt == "" {
		t.Error("expected expires_at in payload when ExpiresAt is set")
	}
	parsed, err := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at not valid RFC3339: %v", err)
	}
	if !parsed.Equal(expiry) {
		t.Errorf("expires_at = %v, want %v", parsed, expiry)
	}
}

func TestWebhookNotifier_ExpiresAtOmittedWhenZero(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(WebhookConfig{URL: srv.URL})
	if err := n.Notify(context.Background(), Event{Type: EventApproved}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unmarshal into a raw map to check key presence.
	var raw map[string]any
	if err := json.Unmarshal(capturedBody, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := raw["expires_at"]; ok {
		t.Error("expected expires_at to be absent when ExpiresAt is zero")
	}
}
