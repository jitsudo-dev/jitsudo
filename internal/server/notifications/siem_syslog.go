//go:build !windows

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package notifications

import (
	"context"
	"fmt"
	"log/syslog"
	"strings"
	"sync"
	"time"
)

// SIEMSyslogConfig holds configuration for the syslog SIEM notifier.
type SIEMSyslogConfig struct {
	// Network is "tcp", "udp", or "" to use the local Unix socket.
	Network string `yaml:"network"`

	// Address is "host:port" for a remote syslog server.
	// Leave empty when Network is "" to use the OS default local socket.
	// Override: JITSUDOD_SIEM_SYSLOG_ADDRESS
	Address string `yaml:"address"`

	// Tag is the syslog process identifier. Defaults to "jitsudo".
	Tag string `yaml:"tag"`

	// Facility is the syslog facility: "auth", "daemon", or "local0"–"local7".
	// Defaults to "auth".
	Facility string `yaml:"facility"`
}

// SIEMSyslogNotifier forwards access events to a syslog server using
// structured key=value messages parseable by any SIEM. It uses a lazy-dial
// strategy and reconnects automatically on write failure.
// Safe for concurrent use.
type SIEMSyslogNotifier struct {
	cfg      SIEMSyslogConfig
	facility syslog.Priority
	mu       sync.Mutex
	writer   *syslog.Writer // nil until first Notify; reconnected on error
}

// NewSIEMSyslogNotifier returns a SIEMSyslogNotifier. The syslog connection
// is established lazily on the first Notify call.
func NewSIEMSyslogNotifier(cfg SIEMSyslogConfig) *SIEMSyslogNotifier {
	tag := cfg.Tag
	if tag == "" {
		tag = "jitsudo"
	}
	return &SIEMSyslogNotifier{
		cfg: SIEMSyslogConfig{
			Network:  cfg.Network,
			Address:  cfg.Address,
			Tag:      tag,
			Facility: cfg.Facility,
		},
		facility: parseFacility(cfg.Facility),
	}
}

// Notify formats the event as a structured syslog message and writes it.
// The context is accepted but unused; log/syslog has no context support.
func (s *SIEMSyslogNotifier) Notify(_ context.Context, event Event) error {
	msg := formatSyslogMsg(event)
	severity := severityFor(event.Type)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writer == nil {
		w, err := syslog.Dial(s.cfg.Network, s.cfg.Address, s.facility|syslog.LOG_INFO, s.cfg.Tag)
		if err != nil {
			return fmt.Errorf("siem_syslog: dial: %w", err)
		}
		s.writer = w
	}

	if err := writeMsg(s.writer, severity, msg); err != nil {
		// Connection may have dropped; attempt one reconnect.
		_ = s.writer.Close()
		s.writer = nil
		w, err2 := syslog.Dial(s.cfg.Network, s.cfg.Address, s.facility|syslog.LOG_INFO, s.cfg.Tag)
		if err2 != nil {
			return fmt.Errorf("siem_syslog: reconnect after write failure: %w", err2)
		}
		s.writer = w
		if err3 := writeMsg(s.writer, severity, msg); err3 != nil {
			return fmt.Errorf("siem_syslog: write after reconnect: %w", err3)
		}
	}
	return nil
}

// writeMsg dispatches to the severity-appropriate syslog.Writer method.
func writeMsg(w *syslog.Writer, severity syslog.Priority, msg string) error {
	switch severity {
	case syslog.LOG_WARNING:
		return w.Warning(msg)
	case syslog.LOG_NOTICE:
		return w.Notice(msg)
	default:
		return w.Info(msg)
	}
}

// formatSyslogMsg produces a structured key=value message for SIEM parsing.
func formatSyslogMsg(event Event) string {
	msg := fmt.Sprintf(
		"type=%s request_id=%s actor=%s provider=%s role=%s scope=%s",
		event.Type, event.RequestID, event.Actor,
		event.Provider, event.Role, event.Scope,
	)
	if event.Reason != "" {
		msg += fmt.Sprintf(" reason=%q", event.Reason)
	}
	if !event.ExpiresAt.IsZero() {
		msg += fmt.Sprintf(" expires_at=%s", event.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return msg
}

// severityFor maps an event type to a syslog severity level.
// Break-glass events are WARNING; denial events are NOTICE; everything else is INFO.
func severityFor(t EventType) syslog.Priority {
	switch t {
	case EventBreakGlass:
		return syslog.LOG_WARNING
	case EventDenied, EventAIDenied:
		return syslog.LOG_NOTICE
	default:
		return syslog.LOG_INFO
	}
}

// parseFacility converts a facility name string to a syslog.Priority constant.
// Unknown names default to LOG_AUTH.
func parseFacility(name string) syslog.Priority {
	switch strings.ToLower(name) {
	case "daemon":
		return syslog.LOG_DAEMON
	case "local0":
		return syslog.LOG_LOCAL0
	case "local1":
		return syslog.LOG_LOCAL1
	case "local2":
		return syslog.LOG_LOCAL2
	case "local3":
		return syslog.LOG_LOCAL3
	case "local4":
		return syslog.LOG_LOCAL4
	case "local5":
		return syslog.LOG_LOCAL5
	case "local6":
		return syslog.LOG_LOCAL6
	case "local7":
		return syslog.LOG_LOCAL7
	default: // "auth" and anything unrecognised
		return syslog.LOG_AUTH
	}
}
