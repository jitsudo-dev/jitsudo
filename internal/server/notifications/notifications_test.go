// License: Elastic License 2.0 (ELv2)
package notifications

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Mock notifier ─────────────────────────────────────────────────────────────

type mockNotifier struct {
	mu      sync.Mutex
	count   int
	lastErr error
	wg      sync.WaitGroup // callers Add(n) before dispatching; Notify calls Done()
}

func (m *mockNotifier) Notify(_ context.Context, _ Event) error {
	defer m.wg.Done()
	m.mu.Lock()
	m.count++
	m.mu.Unlock()
	return m.lastErr
}

func (m *mockNotifier) getCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// ── Dispatcher tests ──────────────────────────────────────────────────────────

func TestDispatcher_NilSafe(t *testing.T) {
	var d *Dispatcher
	// Should not panic.
	d.Notify(context.Background(), Event{Type: EventRequestCreated})
}

func TestDispatcher_FanOut(t *testing.T) {
	n1 := &mockNotifier{}
	n2 := &mockNotifier{}
	n1.wg.Add(1)
	n2.wg.Add(1)
	d := NewDispatcher(n1, n2)

	d.Notify(context.Background(), Event{Type: EventApproved, RequestID: "req-001"})

	n1.wg.Wait()
	n2.wg.Wait()

	if got := n1.getCount(); got != 1 {
		t.Errorf("n1 called %d times, want 1", got)
	}
	if got := n2.getCount(); got != 1 {
		t.Errorf("n2 called %d times, want 1", got)
	}
}

func TestDispatcher_ErrorNotLogged_OtherStillCalled(t *testing.T) {
	errNotifier := &mockNotifier{lastErr: errTest("notify failed")}
	okNotifier := &mockNotifier{}
	errNotifier.wg.Add(1)
	okNotifier.wg.Add(1)
	d := NewDispatcher(errNotifier, okNotifier)

	// Should not panic even when one notifier returns error.
	d.Notify(context.Background(), Event{Type: EventDenied, RequestID: "req-002"})

	errNotifier.wg.Wait()
	okNotifier.wg.Wait()

	if got := errNotifier.getCount(); got != 1 {
		t.Errorf("errNotifier called %d times, want 1", got)
	}
	if got := okNotifier.getCount(); got != 1 {
		t.Errorf("okNotifier called %d times, want 1", got)
	}
}

// ── SlackNotifier tests ───────────────────────────────────────────────────────

func TestSlackNotifier_PostsToWebhook(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(SlackConfig{WebhookURL: srv.URL})
	err := n.Notify(context.Background(), Event{Type: EventApproved, RequestID: "req-001"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("no request captured")
	}
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var payload map[string]string
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if _, ok := payload["text"]; !ok {
		t.Error("expected 'text' field in JSON body")
	}
}

func TestSlackNotifier_ChannelInPayload(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(SlackConfig{WebhookURL: srv.URL, Channel: "#alerts"})
	if err := n.Notify(context.Background(), Event{Type: EventApproved}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ch, ok := payload["channel"]; !ok || ch != "#alerts" {
		t.Errorf("expected channel=#alerts in payload, got %v", payload)
	}
}

func TestSlackNotifier_NonOKResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewSlackNotifier(SlackConfig{WebhookURL: srv.URL})
	err := n.Notify(context.Background(), Event{Type: EventApproved})
	if err == nil {
		t.Error("expected error for non-200 response")
	}
}

// ── formatMessage tests ───────────────────────────────────────────────────────

func TestSlackNotifier_FormatMessage_BreakGlass(t *testing.T) {
	n := NewSlackNotifier(SlackConfig{})
	msg := n.formatMessage(Event{
		Type:   EventBreakGlass,
		Actor:  "admin",
		Role:   "superadmin",
		Scope:  "prod",
		Reason: "emergency",
	})
	if !strings.Contains(msg, "BREAK-GLASS") {
		t.Errorf("expected BREAK-GLASS in message, got: %q", msg)
	}
	// No mention when MentionOnBreakGlass is empty.
	if strings.Contains(msg, "<!") || strings.Contains(msg, "@") {
		t.Errorf("expected no mention syntax in message, got: %q", msg)
	}
}

func TestSlackNotifier_FormatMessage_BreakGlass_WithMention(t *testing.T) {
	n := NewSlackNotifier(SlackConfig{MentionOnBreakGlass: "<!channel>"})
	msg := n.formatMessage(Event{
		Type:  EventBreakGlass,
		Actor: "admin",
	})
	if !strings.HasPrefix(msg, "<!channel>") {
		t.Errorf("expected message to start with mention, got: %q", msg)
	}
	if !strings.Contains(msg, "BREAK-GLASS") {
		t.Errorf("expected BREAK-GLASS in message, got: %q", msg)
	}
}

func TestSlackNotifier_FormatMessage_Approved(t *testing.T) {
	n := NewSlackNotifier(SlackConfig{})
	msg := n.formatMessage(Event{
		Type:      EventApproved,
		RequestID: "req-123",
		Actor:     "approver@example.com",
	})
	if !strings.Contains(msg, "APPROVED") {
		t.Errorf("expected APPROVED in message, got: %q", msg)
	}
	if !strings.Contains(msg, "req-123") {
		t.Errorf("expected requestID in message, got: %q", msg)
	}
}

func TestSlackNotifier_FormatMessage_Denied(t *testing.T) {
	n := NewSlackNotifier(SlackConfig{})
	msg := n.formatMessage(Event{
		Type:      EventDenied,
		RequestID: "req-456",
		Actor:     "approver@example.com",
		Reason:    "policy violation",
	})
	if !strings.Contains(msg, "DENIED") {
		t.Errorf("expected DENIED in message, got: %q", msg)
	}
	if !strings.Contains(msg, "policy violation") {
		t.Errorf("expected reason in message, got: %q", msg)
	}
}

func TestSlackNotifier_FormatMessage_RequestCreated(t *testing.T) {
	n := NewSlackNotifier(SlackConfig{})
	msg := n.formatMessage(Event{
		Type:   EventRequestCreated,
		Actor:  "user@example.com",
		Role:   "admin",
		Scope:  "prod-account",
		Reason: "deploy",
	})
	if !strings.Contains(msg, "user@example.com") {
		t.Errorf("expected actor in message, got: %q", msg)
	}
	if !strings.Contains(msg, "admin") {
		t.Errorf("expected role in message, got: %q", msg)
	}
}

// ── buildMIMEMessage tests ────────────────────────────────────────────────────

func TestBuildMIMEMessage(t *testing.T) {
	from := "from@example.com"
	to := []string{"a@b.com", "c@d.com"}
	subject := "test"
	body := "hello"

	msg := buildMIMEMessage(from, to, subject, body)

	checkHeader := func(name, want string) {
		t.Helper()
		prefix := name + ": "
		for _, line := range strings.Split(msg, "\r\n") {
			if strings.HasPrefix(line, prefix) {
				got := strings.TrimPrefix(line, prefix)
				if got != want {
					t.Errorf("header %s: got %q, want %q", name, got, want)
				}
				return
			}
		}
		t.Errorf("header %q not found in message", name)
	}

	checkHeader("From", from)
	checkHeader("Subject", subject)
	checkHeader("MIME-Version", "1.0")
	checkHeader("Content-Type", "text/plain; charset=utf-8")

	// To header should contain both recipients.
	toFound := false
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, "To: ") {
			toFound = true
			if !strings.Contains(line, "a@b.com") || !strings.Contains(line, "c@d.com") {
				t.Errorf("To header missing recipients, got: %q", line)
			}
		}
	}
	if !toFound {
		t.Error("To header not found")
	}

	// Body should appear after the blank line separator.
	if !strings.Contains(msg, "\r\n\r\n"+body) {
		t.Errorf("expected body after blank line, msg: %q", msg)
	}
}

// ── SMTPNotifier.formatBody tests ─────────────────────────────────────────────

func TestSMTPNotifier_FormatBody_BreakGlass(t *testing.T) {
	n := NewSMTPNotifier(SMTPConfig{})
	body := n.formatBody(Event{
		Type:   EventBreakGlass,
		Actor:  "admin",
		Reason: "emergency",
	})
	if !strings.Contains(body, "WARNING") {
		t.Errorf("expected WARNING in break_glass body, got: %q", body)
	}
}

func TestSMTPNotifier_FormatBody_WithReason(t *testing.T) {
	n := NewSMTPNotifier(SMTPConfig{})
	body := n.formatBody(Event{
		Type:   EventApproved,
		Reason: "testing purposes",
	})
	if !strings.Contains(body, "testing purposes") {
		t.Errorf("expected reason in body, got: %q", body)
	}
}

func TestSMTPNotifier_FormatBody_WithExpiry(t *testing.T) {
	n := NewSMTPNotifier(SMTPConfig{})
	expiry := time.Now().Add(time.Hour)
	body := n.formatBody(Event{
		Type:      EventApproved,
		ExpiresAt: expiry,
	})
	if !strings.Contains(body, "Expires At") {
		t.Errorf("expected 'Expires At' in body when ExpiresAt is non-zero, got: %q", body)
	}
}

func TestSMTPNotifier_FormatBody_NoExpiryWhenZero(t *testing.T) {
	n := NewSMTPNotifier(SMTPConfig{})
	body := n.formatBody(Event{
		Type: EventApproved,
		// ExpiresAt is zero value.
	})
	if strings.Contains(body, "Expires At") {
		t.Errorf("expected 'Expires At' NOT in body when ExpiresAt is zero, got: %q", body)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type errTest string

func (e errTest) Error() string { return string(e) }
