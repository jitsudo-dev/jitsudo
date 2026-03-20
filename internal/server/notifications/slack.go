// License: Elastic License 2.0 (ELv2)
package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SlackConfig holds configuration for the Slack webhook notifier.
type SlackConfig struct {
	// WebhookURL is the incoming webhook URL provided by Slack.
	WebhookURL string `yaml:"webhook_url"`

	// Channel overrides the default channel configured in the webhook.
	// Leave empty to use the webhook's default channel.
	Channel string `yaml:"channel"`

	// MentionOnBreakGlass is prepended to break-glass notifications as a Slack
	// mention string (e.g., "<!channel>" or "@security-team").
	MentionOnBreakGlass string `yaml:"mention_on_break_glass"`
}

// SlackNotifier sends notifications to a Slack incoming webhook.
type SlackNotifier struct {
	cfg    SlackConfig
	client *http.Client
}

// NewSlackNotifier returns a new SlackNotifier with a 5s HTTP timeout.
func NewSlackNotifier(cfg SlackConfig) *SlackNotifier {
	return &SlackNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify POSTs a formatted message to the configured Slack webhook.
func (s *SlackNotifier) Notify(ctx context.Context, event Event) error {
	text := s.formatMessage(event)

	payload := map[string]string{"text": text}
	if s.cfg.Channel != "" {
		payload["channel"] = s.cfg.Channel
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: post webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack: webhook returned %d", resp.StatusCode)
	}
	return nil
}

func (s *SlackNotifier) formatMessage(event Event) string {
	var prefix string
	if event.Type == EventBreakGlass && s.cfg.MentionOnBreakGlass != "" {
		prefix = s.cfg.MentionOnBreakGlass + " "
	}

	switch event.Type {
	case EventBreakGlass:
		return fmt.Sprintf("%s*[BREAK-GLASS]* `%s` invoked break-glass access: role=%s scope=%s reason=%q",
			prefix, event.Actor, event.Role, event.Scope, event.Reason)
	case EventRequestCreated:
		return fmt.Sprintf("`%s` requested access: role=%s scope=%s reason=%q (request %s)",
			event.Actor, event.Role, event.Scope, event.Reason, event.RequestID)
	case EventApproved:
		expiry := ""
		if !event.ExpiresAt.IsZero() {
			expiry = fmt.Sprintf(", expires %s", event.ExpiresAt.UTC().Format(time.RFC3339))
		}
		return fmt.Sprintf("*[APPROVED]* Request `%s` approved by `%s`%s",
			event.RequestID, event.Actor, expiry)
	case EventDenied:
		return fmt.Sprintf("*[DENIED]* Request `%s` denied by `%s`: %s",
			event.RequestID, event.Actor, event.Reason)
	case EventRevoked:
		return fmt.Sprintf("*[REVOKED]* Request `%s` revoked by `%s`: %s",
			event.RequestID, event.Actor, event.Reason)
	case EventExpired:
		return fmt.Sprintf("*[EXPIRED]* Request `%s` has expired (provider=%s)",
			event.RequestID, event.Provider)
	default:
		return fmt.Sprintf("[%s] request=%s actor=%s", event.Type, event.RequestID, event.Actor)
	}
}
