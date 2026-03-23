// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// WebhookConfig holds configuration for a generic outbound webhook notifier.
type WebhookConfig struct {
	// URL is the HTTP(S) endpoint that receives the POST request.
	URL string `yaml:"url"`

	// Headers are added verbatim to every request (e.g. Authorization).
	Headers map[string]string `yaml:"headers"`

	// Secret is an optional HMAC-SHA256 signing key. When set, a
	// X-Jitsudo-Signature-256 header of the form "sha256=<hex>" is included
	// so the receiver can verify payload authenticity (same pattern as GitHub).
	Secret string `yaml:"secret"`

	// Events is an optional allowlist of event types to forward.
	// An empty slice means all events are forwarded.
	Events []string `yaml:"events"`
}

// webhookPayload is the JSON body sent to the configured URL.
type webhookPayload struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Actor     string `json:"actor"`
	Provider  string `json:"provider"`
	Role      string `json:"role"`
	Scope     string `json:"scope"`
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339; omitted when zero
	Timestamp string `json:"timestamp"`            // UTC dispatch time in RFC3339
}

// WebhookNotifier sends structured JSON notifications to a configurable HTTP endpoint.
type WebhookNotifier struct {
	cfg    WebhookConfig
	client *http.Client
}

// NewWebhookNotifier returns a WebhookNotifier with a 5s HTTP timeout.
func NewWebhookNotifier(cfg WebhookConfig) *WebhookNotifier {
	return &WebhookNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify serialises the event as JSON and POSTs it to the configured URL.
// If cfg.Events is non-empty and the event type is not listed, Notify is a no-op.
func (w *WebhookNotifier) Notify(ctx context.Context, event Event) error {
	if len(w.cfg.Events) > 0 && !slices.Contains(w.cfg.Events, string(event.Type)) {
		return nil
	}

	p := webhookPayload{
		Type:      string(event.Type),
		RequestID: event.RequestID,
		Actor:     event.Actor,
		Provider:  event.Provider,
		Role:      event.Role,
		Scope:     event.Scope,
		Reason:    event.Reason,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if !event.ExpiresAt.IsZero() {
		p.ExpiresAt = event.ExpiresAt.UTC().Format(time.RFC3339)
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	for k, v := range w.cfg.Headers {
		req.Header.Set(k, v)
	}

	if w.cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(w.cfg.Secret))
		mac.Write(body)
		req.Header.Set("X-Jitsudo-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // drain to allow connection reuse

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: server returned %d", resp.StatusCode)
	}
	return nil
}
