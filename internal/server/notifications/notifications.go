// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package notifications implements the notification dispatcher for approval
// request alerts. Supported channels: Slack webhook, SMTP email, generic webhook.
package notifications

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// EventType identifies the category of event that triggered the notification.
type EventType string

const (
	EventRequestCreated EventType = "request_created"
	EventApproved       EventType = "approved"
	EventAutoApproved   EventType = "auto_approved"
	EventAIApproved     EventType = "ai_approved"
	EventAIDenied       EventType = "ai_denied"
	EventAIEscalated    EventType = "ai_escalated"
	EventDenied         EventType = "denied"
	EventExpired        EventType = "expired"
	EventRevoked        EventType = "revoked"
	EventBreakGlass     EventType = "break_glass"
)

// Event carries all fields needed to render a notification message.
type Event struct {
	Type      EventType
	RequestID string
	Actor     string // requester, approver, or "system"
	Provider  string
	Role      string
	Scope     string
	Reason    string
	ExpiresAt time.Time // zero if not applicable
}

// Notifier sends a notification for an event. Implementations must be safe
// for concurrent use and must not block the caller for more than a few seconds.
type Notifier interface {
	Notify(ctx context.Context, event Event) error
}

// Dispatcher fans out a single event to all configured notifiers.
// Each notifier is called in its own goroutine with a 10s deadline.
// Errors are logged but never propagated to the caller.
// Calling Notify on a nil Dispatcher is a no-op.
type Dispatcher struct {
	notifiers []Notifier
}

// NewDispatcher returns a Dispatcher that fans out to all provided notifiers.
func NewDispatcher(notifiers ...Notifier) *Dispatcher {
	return &Dispatcher{notifiers: notifiers}
}

// Notify dispatches event to all registered notifiers in separate goroutines.
// Safe to call on a nil *Dispatcher.
func (d *Dispatcher) Notify(ctx context.Context, event Event) {
	if d == nil || len(d.notifiers) == 0 {
		return
	}
	for _, n := range d.notifiers {
		go func(notifier Notifier) {
			nctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := notifier.Notify(nctx, event); err != nil {
				log.Warn().
					Err(err).
					Str("event_type", string(event.Type)).
					Str("request_id", event.RequestID).
					Msg("notification failed")
			}
		}(n)
	}
}
