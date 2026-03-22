//go:build !windows

// License: Elastic License 2.0 (ELv2)
package notifications

import (
	"context"
	"io"
	"log/syslog"
	"net"
	"strings"
	"testing"
	"time"
)

// startSyslogListener starts a TCP server that accepts one connection, reads
// all data until EOF, and returns it on the returned channel.
func startSyslogListener(t *testing.T) (addr string, msgCh <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startSyslogListener: listen: %v", err)
	}
	ch := make(chan string, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			ch <- ""
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(conn)
		ch <- string(data)
	}()
	return ln.Addr().String(), ch
}

// startMultiSyslogListener starts a TCP server that accepts multiple connections,
// accumulating all received data into the returned channel (one string per connection).
func startMultiSyslogListener(t *testing.T, n int) (addr string, ln net.Listener, msgCh <-chan string) {
	t.Helper()
	var err error
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startMultiSyslogListener: listen: %v", err)
	}
	ch := make(chan string, n)
	go func() {
		for i := 0; i < n; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			data, _ := io.ReadAll(conn)
			conn.Close()
			ch <- string(data)
		}
	}()
	return ln.Addr().String(), ln, ch
}

func TestSIEMSyslogNotifier_ConnectsAndWrites(t *testing.T) {
	addr, msgCh := startSyslogListener(t)

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{
		Network: "tcp",
		Address: addr,
		Tag:     "jitsudo-test",
	})
	if err := n.Notify(context.Background(), Event{
		Type:      EventApproved,
		RequestID: "req-001",
		Actor:     "user@example.com",
		Provider:  "aws",
		Role:      "ReadOnly",
		Scope:     "123456789012",
	}); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	// Close the writer so the listener receives EOF and we can read the message.
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()

	msg := <-msgCh
	if msg == "" {
		t.Fatal("no data received by syslog listener")
	}
}

func TestSIEMSyslogNotifier_MessageContainsFields(t *testing.T) {
	addr, msgCh := startSyslogListener(t)

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})
	_ = n.Notify(context.Background(), Event{
		Type:      EventApproved,
		RequestID: "req-abc",
		Actor:     "alice@example.com",
		Provider:  "gcp",
		Role:      "viewer",
		Scope:     "my-project",
	})
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()

	msg := <-msgCh
	for _, want := range []string{
		"type=approved",
		"request_id=req-abc",
		"actor=alice@example.com",
		"provider=gcp",
		"role=viewer",
		"scope=my-project",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q; got: %s", want, msg)
		}
	}
}

func TestSIEMSyslogNotifier_ReasonQuoted(t *testing.T) {
	addr, msgCh := startSyslogListener(t)

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})
	_ = n.Notify(context.Background(), Event{
		Type:   EventApproved,
		Reason: "urgent incident fix",
	})
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()

	msg := <-msgCh
	if !strings.Contains(msg, `reason="urgent incident fix"`) {
		t.Errorf("expected quoted reason in message; got: %s", msg)
	}
}

func TestSIEMSyslogNotifier_ExpiresAtInMessage(t *testing.T) {
	addr, msgCh := startSyslogListener(t)

	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})
	_ = n.Notify(context.Background(), Event{
		Type:      EventApproved,
		ExpiresAt: expiry,
	})
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()

	msg := <-msgCh
	want := "expires_at=" + expiry.Format(time.RFC3339)
	if !strings.Contains(msg, want) {
		t.Errorf("expected %q in message; got: %s", want, msg)
	}
}

func TestSIEMSyslogNotifier_DefaultTag(t *testing.T) {
	// When Tag is empty, NewSIEMSyslogNotifier should default to "jitsudo".
	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{})
	if n.cfg.Tag != "jitsudo" {
		t.Errorf("default tag = %q, want jitsudo", n.cfg.Tag)
	}
}

func TestSIEMSyslogNotifier_DialError(t *testing.T) {
	// Start a listener, capture its address, then close it so syslog.Dial fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // close immediately so the port is not listening when Notify is called

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})
	if err := n.Notify(context.Background(), Event{Type: EventApproved}); err == nil {
		t.Error("expected error when syslog server is unreachable")
	}
}

func TestSIEMSyslogNotifier_BreakGlassSeverity(t *testing.T) {
	// break_glass events should hit the Warning path in writeMsg.
	addr, msgCh := startSyslogListener(t)

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})
	if err := n.Notify(context.Background(), Event{
		Type:      EventBreakGlass,
		RequestID: "req-bg",
		Actor:     "alice@example.com",
	}); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()

	msg := <-msgCh
	if !strings.Contains(msg, "type=break_glass") {
		t.Errorf("expected type=break_glass in message; got: %s", msg)
	}
}

func TestSIEMSyslogNotifier_DeniedSeverity(t *testing.T) {
	// denied events should hit the Notice path in writeMsg.
	addr, msgCh := startSyslogListener(t)

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})
	if err := n.Notify(context.Background(), Event{
		Type:      EventDenied,
		RequestID: "req-denied",
		Actor:     "approver@example.com",
	}); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()

	msg := <-msgCh
	if !strings.Contains(msg, "type=denied") {
		t.Errorf("expected type=denied in message; got: %s", msg)
	}
}

func TestSIEMSyslogNotifier_Reconnect(t *testing.T) {
	// Simulate a dropped connection: first write succeeds, then we sever the
	// connection, then the next Notify should reconnect transparently.
	addr, ln, _ := startMultiSyslogListener(t, 2)
	defer ln.Close()

	n := NewSIEMSyslogNotifier(SIEMSyslogConfig{Network: "tcp", Address: addr})

	// First call establishes the connection.
	if err := n.Notify(context.Background(), Event{Type: EventApproved, RequestID: "req-1"}); err != nil {
		t.Fatalf("first Notify error: %v", err)
	}

	// Sever the underlying connection to trigger the reconnect path.
	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
		n.writer = nil
	}
	n.mu.Unlock()

	// Second call should reconnect and succeed.
	if err := n.Notify(context.Background(), Event{Type: EventApproved, RequestID: "req-2"}); err != nil {
		t.Fatalf("second Notify after reconnect error: %v", err)
	}

	n.mu.Lock()
	if n.writer != nil {
		_ = n.writer.Close()
	}
	n.mu.Unlock()
}

// ── Unit tests for helpers ────────────────────────────────────────────────────

func TestFormatSyslogMsg_BasicFields(t *testing.T) {
	msg := formatSyslogMsg(Event{
		Type:      EventApproved,
		RequestID: "req-1",
		Actor:     "bob@example.com",
		Provider:  "azure",
		Role:      "Contributor",
		Scope:     "/subscriptions/sub-id",
	})
	for _, want := range []string{"type=approved", "request_id=req-1", "actor=bob@example.com", "provider=azure", "role=Contributor", "scope=/subscriptions/sub-id"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %q", want, msg)
		}
	}
	if strings.Contains(msg, "reason=") {
		t.Errorf("unexpected reason= in message with empty reason: %q", msg)
	}
	if strings.Contains(msg, "expires_at=") {
		t.Errorf("unexpected expires_at= in message with zero ExpiresAt: %q", msg)
	}
}

func TestFormatSyslogMsg_WithReason(t *testing.T) {
	msg := formatSyslogMsg(Event{Reason: "on call rotation"})
	if !strings.Contains(msg, `reason="on call rotation"`) {
		t.Errorf("expected quoted reason in %q", msg)
	}
}

func TestFormatSyslogMsg_WithExpiresAt(t *testing.T) {
	expiry := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	msg := formatSyslogMsg(Event{ExpiresAt: expiry})
	want := "expires_at=2026-01-15T12:00:00Z"
	if !strings.Contains(msg, want) {
		t.Errorf("expected %q in %q", want, msg)
	}
}

func TestSeverityFor_BreakGlass(t *testing.T) {
	if got := severityFor(EventBreakGlass); got != syslog.LOG_WARNING {
		t.Errorf("severityFor(EventBreakGlass) = %v, want LOG_WARNING", got)
	}
}

func TestSeverityFor_Denied(t *testing.T) {
	for _, et := range []EventType{EventDenied, EventAIDenied} {
		if got := severityFor(et); got != syslog.LOG_NOTICE {
			t.Errorf("severityFor(%q) = %v, want LOG_NOTICE", et, got)
		}
	}
}

func TestSeverityFor_Info(t *testing.T) {
	for _, et := range []EventType{EventApproved, EventAutoApproved, EventAIApproved, EventExpired, EventRevoked} {
		if got := severityFor(et); got != syslog.LOG_INFO {
			t.Errorf("severityFor(%q) = %v, want LOG_INFO", et, got)
		}
	}
}

func TestParseFacility(t *testing.T) {
	cases := []struct {
		name string
		want syslog.Priority
	}{
		{"auth", syslog.LOG_AUTH},
		{"AUTH", syslog.LOG_AUTH},
		{"daemon", syslog.LOG_DAEMON},
		{"local0", syslog.LOG_LOCAL0},
		{"local1", syslog.LOG_LOCAL1},
		{"local2", syslog.LOG_LOCAL2},
		{"local3", syslog.LOG_LOCAL3},
		{"local4", syslog.LOG_LOCAL4},
		{"local5", syslog.LOG_LOCAL5},
		{"local6", syslog.LOG_LOCAL6},
		{"local7", syslog.LOG_LOCAL7},
		{"unknown", syslog.LOG_AUTH}, // fallback
		{"", syslog.LOG_AUTH},        // fallback
	}
	for _, tc := range cases {
		if got := parseFacility(tc.name); got != tc.want {
			t.Errorf("parseFacility(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
