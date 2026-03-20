// License: Elastic License 2.0 (ELv2)
package notifications

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig holds configuration for the SMTP email notifier.
type SMTPConfig struct {
	// Host is the SMTP server hostname.
	Host string `yaml:"host"`

	// Port is the SMTP server port (typically 587 for STARTTLS or 465 for TLS).
	Port int `yaml:"port"`

	// Username is the SMTP authentication username.
	Username string `yaml:"username"`

	// Password is the SMTP authentication password.
	Password string `yaml:"password"`

	// From is the sender email address.
	From string `yaml:"from"`

	// To is the list of recipient email addresses.
	To []string `yaml:"to"`
}

// SMTPNotifier sends notifications via email using SMTP.
type SMTPNotifier struct {
	cfg SMTPConfig
}

// NewSMTPNotifier returns a new SMTPNotifier.
func NewSMTPNotifier(cfg SMTPConfig) *SMTPNotifier {
	return &SMTPNotifier{cfg: cfg}
}

// Notify sends an email notification. The context deadline is respected where
// possible, though net/smtp does not natively support context cancellation.
func (s *SMTPNotifier) Notify(_ context.Context, event Event) error {
	subject := fmt.Sprintf("[jitsudo] %s – %s", event.Type, event.RequestID)
	body := s.formatBody(event)

	msg := buildMIMEMessage(s.cfg.From, s.cfg.To, subject, body)

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}

	if err := smtp.SendMail(addr, auth, s.cfg.From, s.cfg.To, []byte(msg)); err != nil {
		return fmt.Errorf("smtp: send mail: %w", err)
	}
	return nil
}

func (s *SMTPNotifier) formatBody(event Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Event:      %s\n", event.Type)
	fmt.Fprintf(&b, "Request ID: %s\n", event.RequestID)
	fmt.Fprintf(&b, "Actor:      %s\n", event.Actor)
	fmt.Fprintf(&b, "Provider:   %s\n", event.Provider)
	fmt.Fprintf(&b, "Role:       %s\n", event.Role)
	fmt.Fprintf(&b, "Scope:      %s\n", event.Scope)
	if event.Reason != "" {
		fmt.Fprintf(&b, "Reason:     %s\n", event.Reason)
	}
	if !event.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "Expires At: %s\n", event.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if event.Type == EventBreakGlass {
		b.WriteString("\nWARNING: This was a break-glass access request that bypassed normal approval.\n")
		b.WriteString("Please review immediately.\n")
	}
	return b.String()
}

// buildMIMEMessage assembles a minimal RFC 2822 email message.
func buildMIMEMessage(from string, to []string, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "\r\n")
	b.WriteString(body)
	return b.String()
}
