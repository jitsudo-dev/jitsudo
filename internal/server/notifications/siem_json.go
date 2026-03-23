// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
)

// SIEMConfig is the top-level SIEM block in NotificationsCfg.
// It bundles the JSON streaming and syslog sub-configurations.
type SIEMConfig struct {
	JSON   *SIEMJSONConfig   `yaml:"json"`
	Syslog *SIEMSyslogConfig `yaml:"syslog"`
}

// SIEMJSONConfig holds configuration for the SIEM JSON streaming notifier.
// It POSTs individual events as JSON to an HTTP ingest endpoint such as
// Splunk HEC, Elasticsearch, or any compatible receiver.
type SIEMJSONConfig struct {
	// URL is the HTTP(S) endpoint that receives the POST request.
	// Override: JITSUDOD_SIEM_JSON_URL
	URL string `yaml:"url"`

	// Headers are added verbatim to every request (e.g. Authorization: Bearer <token>).
	Headers map[string]string `yaml:"headers"`

	// Events is an optional allowlist of event types to forward.
	// An empty slice means all events are forwarded.
	Events []string `yaml:"events"`
}

// siemJSONPayload is the JSON body sent to the configured SIEM endpoint.
// It extends the generic webhook payload with deduplication fields
// (source, schema_version, event_id) that help SIEM systems correlate events.
type siemJSONPayload struct {
	Source        string `json:"source"`         // always "jitsudo"
	SchemaVersion string `json:"schema_version"` // always "1"
	EventID       string `json:"event_id"`       // UUID v4 for deduplication
	Type          string `json:"type"`
	RequestID     string `json:"request_id"`
	Actor         string `json:"actor"`
	Provider      string `json:"provider"`
	Role          string `json:"role"`
	Scope         string `json:"scope"`
	Reason        string `json:"reason"`
	ExpiresAt     string `json:"expires_at,omitempty"` // RFC3339; omitted when zero
	Timestamp     string `json:"timestamp"`            // UTC dispatch time in RFC3339
}

// SIEMJSONNotifier sends structured JSON notifications to a configurable HTTP
// SIEM ingest endpoint. Each event is a self-contained POST with a UUID
// event_id for deduplication. Safe for concurrent use.
type SIEMJSONNotifier struct {
	cfg    SIEMJSONConfig
	client *http.Client
}

// NewSIEMJSONNotifier returns a SIEMJSONNotifier with a 5s HTTP timeout.
func NewSIEMJSONNotifier(cfg SIEMJSONConfig) *SIEMJSONNotifier {
	return &SIEMJSONNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify serialises the event as JSON and POSTs it to the configured SIEM endpoint.
// If cfg.Events is non-empty and the event type is not listed, Notify is a no-op.
func (s *SIEMJSONNotifier) Notify(ctx context.Context, event Event) error {
	if len(s.cfg.Events) > 0 && !slices.Contains(s.cfg.Events, string(event.Type)) {
		return nil
	}

	p := siemJSONPayload{
		Source:        "jitsudo",
		SchemaVersion: "1",
		EventID:       uuid.New().String(),
		Type:          string(event.Type),
		RequestID:     event.RequestID,
		Actor:         event.Actor,
		Provider:      event.Provider,
		Role:          event.Role,
		Scope:         event.Scope,
		Reason:        event.Reason,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	if !event.ExpiresAt.IsZero() {
		p.ExpiresAt = event.ExpiresAt.UTC().Format(time.RFC3339)
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("siem_json: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("siem_json: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("siem_json: post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // drain to allow connection reuse

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("siem_json: server returned %d", resp.StatusCode)
	}
	return nil
}
